package netgames

// Minecraft Java 版服务器列表 Ping 客户端。
//
// 仅使用标准库（net / encoding/binary / encoding/json / unicode/utf16），
// 不引入任何第三方依赖：
//   - 现代协议（1.7+）：TCP 连接 → handshake(0x00, protocol=-1, next_state=1)
//     → status(0x00) → 读取 JSON（players.online / players.max / version.*）
//     → 可选 ping(0x01)+pong 测量延迟。
//   - 兼容回退：现代协议失败时依次尝试 legacy 0xFE 0x01（1.6+ 与 1.3 风格响应）
//     与 0xFA MC|PingHost 插件消息（1.4/1.5）。
//
// 协议细节参考 wiki.vg（Server List Ping 一节）。

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	mcPingTimeout      = 5 * time.Second
	mcDefaultPort      = 25565
	mcProtocolVersion  = -1 // 状态查询用任意协议号：-1 表示"只做 ping"（wiki.vg 推荐值）
	mcLegacyProtocol   = 74 // legacy 插件消息携带的协议号（1.5.2）
	mcPingWorkerCount  = 8
	mcMaxVersionLength = 100
	mcMaxErrorLength   = 500
)

// MCPingResult 是单次 ping 的结果。
type MCPingResult struct {
	Online      bool   `json:"online"`
	OnlineCount uint32 `json:"online_count"`
	MaxPlayers  uint32 `json:"max_players"`
	VersionName string `json:"version_name"`
	Protocol    int    `json:"protocol"`
	LatencyMS   int64  `json:"latency_ms"`
	Description string `json:"description"`
}

// NormalizeMinecraftHost 规范化主机名：去空白、转小写、去末尾点。
func NormalizeMinecraftHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

// NormalizeMinecraftAddress 解析 "host[:port]" 形式的地址。
// 未给出端口时使用 25565；IPv6 字面量需使用方括号（如 [::1]:25565）。
func NormalizeMinecraftAddress(input string) (host string, port uint16, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", 0, errors.New("服务器地址不能为空")
	}
	if strings.HasPrefix(input, "[") {
		end := strings.Index(input, "]")
		if end < 0 {
			return "", 0, errors.New("IPv6 地址缺少右方括号")
		}
		host = NormalizeMinecraftHost(input[1:end])
		if host == "" {
			return "", 0, errors.New("服务器地址无效")
		}
		rest := strings.TrimPrefix(input[end+1:], ":")
		if rest != "" {
			parsed, parseErr := parseMinecraftPort(rest)
			if parseErr != nil {
				return "", 0, parseErr
			}
			return host, parsed, nil
		}
		return host, mcDefaultPort, nil
	}
	if hostText, portText, found := strings.Cut(input, ":"); found {
		host = NormalizeMinecraftHost(hostText)
		if host == "" {
			return "", 0, errors.New("服务器地址无效")
		}
		parsed, parseErr := parseMinecraftPort(portText)
		if parseErr != nil {
			return "", 0, parseErr
		}
		return host, parsed, nil
	}
	host = NormalizeMinecraftHost(input)
	if host == "" {
		return "", 0, errors.New("服务器地址无效")
	}
	return host, mcDefaultPort, nil
}

func parseMinecraftPort(text string) (uint16, error) {
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || value < 1 || value > 65535 {
		return 0, fmt.Errorf("端口无效：%q", text)
	}
	return uint16(value), nil
}

// MinecraftServerKey 生成规范化唯一键 "host:port"（IPv6 加方括号）。
func MinecraftServerKey(host string, port uint16) string {
	host = NormalizeMinecraftHost(host)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// SplitMinecraftAddress 拆分 "host:port" 键。
func SplitMinecraftAddress(key string) (string, uint16, error) {
	return NormalizeMinecraftAddress(key)
}

// PingMCServer 对指定服务器执行 Server List Ping，优先现代协议，失败后回退 legacy。
func PingMCServer(ctx context.Context, host string, port uint16) (MCPingResult, error) {
	host = NormalizeMinecraftHost(host)
	if host == "" {
		return MCPingResult{}, errors.New("服务器地址无效")
	}
	result, err := pingModern(ctx, host, port)
	if err == nil {
		return result, nil
	}
	legacy, legacyErr := pingLegacy(ctx, host, port)
	if legacyErr == nil {
		return legacy, nil
	}
	return MCPingResult{}, fmt.Errorf("现代协议 ping 失败：%v；legacy ping 失败：%v", err, legacyErr)
}

// ---------------------------------------------------------------------------
// 现代协议（1.7+）
// ---------------------------------------------------------------------------

type mcStatusJSON struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Online uint32 `json:"online"`
		Max    uint32 `json:"max"`
	} `json:"players"`
	Description json.RawMessage `json:"description"`
}

func pingModern(ctx context.Context, host string, port uint16) (MCPingResult, error) {
	dialer := net.Dialer{Timeout: mcPingTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return MCPingResult{}, fmt.Errorf("连接失败：%w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(mcPingTimeout))

	// handshake：packet_id=0x00, protocol_version=-1, host, port, next_state=1
	var packet bytes.Buffer
	writeMCVarInt(&packet, 0x00)
	writeMCVarInt(&packet, mcProtocolVersion)
	writeMCString(&packet, host)
	_ = binary.Write(&packet, binary.BigEndian, port)
	writeMCVarInt(&packet, 1)

	var frame bytes.Buffer
	writeMCVarInt(&frame, packet.Len())
	frame.Write(packet.Bytes())
	if _, err := conn.Write(frame.Bytes()); err != nil {
		return MCPingResult{}, fmt.Errorf("发送 handshake 失败：%w", err)
	}

	// status request：packet_id=0x00（空载荷）
	var request bytes.Buffer
	writeMCVarInt(&request, 1)
	writeMCVarInt(&request, 0x00)
	if _, err := conn.Write(request.Bytes()); err != nil {
		return MCPingResult{}, fmt.Errorf("发送 status 请求失败：%w", err)
	}

	started := time.Now()
	reader := bufio.NewReader(conn)
	length, err := readMCVarInt(reader)
	if err != nil {
		return MCPingResult{}, fmt.Errorf("读取 status 响应长度失败：%w", err)
	}
	if length < 1 || length > 1<<20 {
		return MCPingResult{}, fmt.Errorf("status 响应长度异常：%d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return MCPingResult{}, fmt.Errorf("读取 status 响应失败：%w", err)
	}
	payloadReader := bytes.NewReader(payload)
	packetID, err := readMCVarInt(payloadReader)
	if err != nil {
		return MCPingResult{}, fmt.Errorf("解析 status 响应包 ID 失败：%w", err)
	}
	if packetID != 0x00 {
		return MCPingResult{}, fmt.Errorf("status 响应包 ID 异常：0x%02X", packetID)
	}
	statusText, err := readMCString(payloadReader)
	if err != nil {
		return MCPingResult{}, fmt.Errorf("解析 status JSON 失败：%w", err)
	}
	var status mcStatusJSON
	if err := json.Unmarshal([]byte(statusText), &status); err != nil {
		return MCPingResult{}, fmt.Errorf("status JSON 无法解析：%w", err)
	}

	// 可选 ping/pong 测延迟（部分服务器不回 pong，此时用 status 响应时间）。
	latency := time.Since(started).Milliseconds()
	if pong, pingErr := mcPingPong(reader, conn, started); pingErr == nil {
		latency = pong
	}

	description := mcDescriptionText(status.Description)
	return MCPingResult{
		Online:      true,
		OnlineCount: status.Players.Online,
		MaxPlayers:  status.Players.Max,
		VersionName: truncateRunes(status.Version.Name, mcMaxVersionLength),
		Protocol:    status.Version.Protocol,
		LatencyMS:   latency,
		Description: description,
	}, nil
}

// mcPingPong 发送 ping(0x01)+8 字节时间戳并等待 pong，返回往返毫秒数。
func mcPingPong(reader *bufio.Reader, conn net.Conn, started time.Time) (int64, error) {
	var payload bytes.Buffer
	writeMCVarInt(&payload, 9) // 长度：1(packet_id) + 8(long)
	writeMCVarInt(&payload, 0x01)
	timestamp := started.UnixMilli()
	var stamp [8]byte
	binary.BigEndian.PutUint64(stamp[:], uint64(timestamp))
	payload.Write(stamp[:])
	if _, err := conn.Write(payload.Bytes()); err != nil {
		return 0, err
	}
	length, err := readMCVarInt(reader)
	if err != nil {
		return 0, err
	}
	if length != 9 {
		return 0, fmt.Errorf("pong 长度异常：%d", length)
	}
	response := make([]byte, 9)
	if _, err := io.ReadFull(reader, response); err != nil {
		return 0, err
	}
	if response[0] != 0x01 {
		return 0, fmt.Errorf("pong 包 ID 异常：0x%02X", response[0])
	}
	if !bytes.Equal(response[1:], stamp[:]) {
		return 0, errors.New("pong 时间戳不匹配")
	}
	return time.Since(started).Milliseconds(), nil
}

func mcDescriptionText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return truncateRunes(strings.TrimSpace(text), mcMaxVersionLength)
		}
		return ""
	}
	var object struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &object) == nil && strings.TrimSpace(object.Text) != "" {
		return truncateRunes(strings.TrimSpace(object.Text), mcMaxVersionLength)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Legacy 协议（0xFE / 0xFA）
// ---------------------------------------------------------------------------

func pingLegacy(ctx context.Context, host string, port uint16) (MCPingResult, error) {
	// 1.6+ / 1.3 风格：0xFE 0x01
	result, err := pingLegacyFE(ctx, host, port)
	if err == nil {
		return result, nil
	}
	firstErr := err
	// 1.4 / 1.5：0xFA MC|PingHost 插件消息
	result, err = pingLegacyPluginMessage(ctx, host, port)
	if err == nil {
		return result, nil
	}
	return MCPingResult{}, fmt.Errorf("0xFE ping 失败：%v；0xFA ping 失败：%v", firstErr, err)
}

func pingLegacyFE(ctx context.Context, host string, port uint16) (MCPingResult, error) {
	dialer := net.Dialer{Timeout: mcPingTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return MCPingResult{}, fmt.Errorf("连接失败：%w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(mcPingTimeout))

	started := time.Now()
	if _, err := conn.Write([]byte{0xFE, 0x01}); err != nil {
		return MCPingResult{}, fmt.Errorf("发送 0xFE 请求失败：%w", err)
	}
	reader := bufio.NewReader(conn)
	first, err := reader.ReadByte()
	if err != nil {
		return MCPingResult{}, fmt.Errorf("读取响应头失败：%w", err)
	}
	if first != 0xFF {
		return MCPingResult{}, fmt.Errorf("响应包 ID 异常：0x%02X", first)
	}
	var length uint16
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return MCPingResult{}, fmt.Errorf("读取响应长度失败：%w", err)
	}
	raw := make([]byte, int(length)*2)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return MCPingResult{}, fmt.Errorf("读取响应内容失败：%w", err)
	}
	text, err := decodeMinecraftUTF16BE(raw)
	if err != nil {
		return MCPingResult{}, fmt.Errorf("解码响应失败：%w", err)
	}
	result, err := parseLegacyPingResponse(text)
	if err != nil {
		return MCPingResult{}, err
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	result.Online = true
	if result.VersionName == "" {
		result.VersionName = "Legacy"
	}
	return result, nil
}

func pingLegacyPluginMessage(ctx context.Context, host string, port uint16) (MCPingResult, error) {
	dialer := net.Dialer{Timeout: mcPingTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		return MCPingResult{}, fmt.Errorf("连接失败：%w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(mcPingTimeout))

	channel := "MC|PingHost"
	var data bytes.Buffer
	data.WriteByte(mcLegacyProtocol) // 协议版本
	writeMCShortString(&data, host)  // 主机名（2 字节大端长度前缀）
	_ = binary.Write(&data, binary.BigEndian, int32(port))

	var request bytes.Buffer
	request.WriteByte(0xFA) // 插件消息包
	writeMCShortString(&request, channel)
	writeMCShortString(&request, data.String())

	started := time.Now()
	if _, err := conn.Write(request.Bytes()); err != nil {
		return MCPingResult{}, fmt.Errorf("发送 0xFA 请求失败：%w", err)
	}
	reader := bufio.NewReader(conn)
	first, err := reader.ReadByte()
	if err != nil {
		return MCPingResult{}, fmt.Errorf("读取响应头失败：%w", err)
	}
	if first != 0xFF {
		return MCPingResult{}, fmt.Errorf("响应包 ID 异常：0x%02X", first)
	}
	var length uint16
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return MCPingResult{}, fmt.Errorf("读取响应长度失败：%w", err)
	}
	raw := make([]byte, int(length)*2)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return MCPingResult{}, fmt.Errorf("读取响应内容失败：%w", err)
	}
	text, err := decodeMinecraftUTF16BE(raw)
	if err != nil {
		return MCPingResult{}, fmt.Errorf("解码响应失败：%w", err)
	}
	result, err := parseLegacyPingResponse(text)
	if err != nil {
		return MCPingResult{}, err
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	result.Online = true
	if result.VersionName == "" {
		result.VersionName = "Legacy"
	}
	return result, nil
}

// parseLegacyPingResponse 解析 0xFF 响应文本。
// 1.6+：§1\0<protocol>\0<version>\0<motd>\0<online>\0<max>
// 1.3/1.4/1.5：<motd>\0<online>\0<max>
func parseLegacyPingResponse(text string) (MCPingResult, error) {
	fields := strings.Split(text, "\x00")
	for index := range fields {
		fields[index] = strings.TrimSpace(fields[index])
	}
	result := MCPingResult{}
	if len(fields) >= 6 && strings.HasPrefix(fields[0], "\u00a71") {
		protocol, err := strconv.Atoi(fields[1])
		if err != nil {
			return result, fmt.Errorf("legacy 协议号无效：%q", fields[1])
		}
		result.Protocol = protocol
		result.VersionName = truncateRunes(fields[2], mcMaxVersionLength)
		result.Description = truncateRunes(fields[3], mcMaxVersionLength)
		online, err1 := strconv.ParseUint(fields[4], 10, 32)
		maximum, err2 := strconv.ParseUint(fields[5], 10, 32)
		if err1 != nil || err2 != nil {
			return result, errors.New("legacy 在线人数无效")
		}
		result.OnlineCount = uint32(online)
		result.MaxPlayers = uint32(maximum)
		return result, nil
	}
	if len(fields) >= 3 {
		result.Description = truncateRunes(fields[0], mcMaxVersionLength)
		online, err1 := strconv.ParseUint(fields[1], 10, 32)
		maximum, err2 := strconv.ParseUint(fields[2], 10, 32)
		if err1 != nil || err2 != nil {
			return result, errors.New("legacy 在线人数无效")
		}
		result.OnlineCount = uint32(online)
		result.MaxPlayers = uint32(maximum)
		return result, nil
	}
	return result, errors.New("legacy 响应格式无法识别")
}

func decodeMinecraftUTF16BE(data []byte) (string, error) {
	if len(data)%2 != 0 {
		return "", errors.New("UTF-16BE 数据长度必须为偶数")
	}
	units := make([]uint16, len(data)/2)
	for index := range units {
		units[index] = binary.BigEndian.Uint16(data[index*2:])
	}
	return string(utf16.Decode(units)), nil
}

// ---------------------------------------------------------------------------
// VarInt / String 编解码（Minecraft 网络协议）
// ---------------------------------------------------------------------------

func writeMCVarInt(buffer *bytes.Buffer, value int) {
	unsigned := uint32(value)
	for {
		current := byte(unsigned & 0x7F)
		unsigned >>= 7
		if unsigned != 0 {
			current |= 0x80
		}
		buffer.WriteByte(current)
		if unsigned == 0 {
			return
		}
	}
}

func readMCVarInt(reader io.ByteReader) (int, error) {
	var value uint32
	for index := 0; index < 5; index++ {
		current, err := reader.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= uint32(current&0x7F) << (7 * index)
		if current&0x80 == 0 {
			// Minecraft VarInt 是带符号 int32，第 5 字节只取低 1 位，符号扩展
			return int(int32(value)), nil
		}
	}
	return 0, errors.New("VarInt 超过 5 字节")
}

func writeMCString(buffer *bytes.Buffer, value string) {
	writeMCVarInt(buffer, len(value))
	buffer.WriteString(value)
}

// writeMCShortString 写入 legacy 协议要求的 2 字节大端长度前缀字符串。
func writeMCShortString(buffer *bytes.Buffer, value string) {
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(value)))
	buffer.Write(length[:])
	buffer.WriteString(value)
}

func readMCString(reader io.Reader) (string, error) {
	byteReader, ok := reader.(io.ByteReader)
	if !ok {
		byteReader = bufio.NewReader(reader)
	}
	length, err := readMCVarInt(byteReader)
	if err != nil {
		return "", err
	}
	if length < 0 || length > 1<<20 {
		return "", fmt.Errorf("字符串长度异常：%d", length)
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", err
	}
	return string(raw), nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}
