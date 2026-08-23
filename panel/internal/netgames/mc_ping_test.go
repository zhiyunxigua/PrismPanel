package netgames

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// ---------------------------------------------------------------------------
// VarInt / 地址解析
// ---------------------------------------------------------------------------

func TestMCVarIntRoundTrip(t *testing.T) {
	cases := []int{0, 1, 127, 128, 255, 300, 25565, 1<<31 - 1, -1}
	for _, value := range cases {
		var buffer bytes.Buffer
		writeMCVarInt(&buffer, value)
		reader := bufio.NewReader(&buffer)
		decoded, err := readMCVarInt(reader)
		if err != nil {
			t.Fatalf("readMCVarInt(%d): %v", value, err)
		}
		if decoded != value {
			t.Fatalf("readMCVarInt(%d) = %d", value, decoded)
		}
	}
}

func TestReadMCVarIntRejectsOversize(t *testing.T) {
	// 5 个字节都带 continuation bit → 非法
	_, err := readMCVarInt(bufio.NewReader(bytes.NewReader([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF})))
	if err == nil {
		t.Fatal("expected oversize varint to be rejected")
	}
}

func TestNormalizeMinecraftAddress(t *testing.T) {
	cases := []struct {
		in       string
		host     string
		port     uint16
		hasError bool
	}{
		{"play.example.com", "play.example.com", 25565, false},
		{"  Play.Example.COM. ", "play.example.com", 25565, false},
		{"mc.example.org:19132", "mc.example.org", 19132, false},
		{"127.0.0.1", "127.0.0.1", 25565, false},
		{"127.0.0.1:25565", "127.0.0.1", 25565, false},
		{"[::1]:25566", "::1", 25566, false},
		{"[2001:db8::1]", "2001:db8::1", 25565, false},
		{"", "", 0, true},
		{"host:0", "", 0, true},
		{"host:99999", "", 0, true},
		{"host:abc", "", 0, true},
		{"[::1", "", 0, true},
	}
	for _, c := range cases {
		host, port, err := NormalizeMinecraftAddress(c.in)
		if c.hasError {
			if err == nil {
				t.Fatalf("NormalizeMinecraftAddress(%q) expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NormalizeMinecraftAddress(%q): %v", c.in, err)
		}
		if host != c.host || port != c.port {
			t.Fatalf("NormalizeMinecraftAddress(%q) = %q:%d, want %q:%d", c.in, host, port, c.host, c.port)
		}
	}
}

func TestMinecraftServerKey(t *testing.T) {
	if key := MinecraftServerKey("Play.Example.COM", 25565); key != "play.example.com:25565" {
		t.Fatalf("key = %q", key)
	}
	if key := MinecraftServerKey("::1", 25566); key != "[::1]:25566" {
		t.Fatalf("ipv6 key = %q", key)
	}
}

// ---------------------------------------------------------------------------
// 假服务器
// ---------------------------------------------------------------------------

// startFakeModernMCServer 启动一个实现现代协议 Server List Ping 的假服务器。
// 返回 127.0.0.1 上的监听端口与关闭函数。
func startFakeModernMCServer(t *testing.T, online, maxPlayers uint32, version string, protocol int) (uint16, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleFakeModernConnection(conn, online, maxPlayers, version, protocol)
		}
	}()
	return port, func() { _ = listener.Close() }
}

func handleFakeModernConnection(conn net.Conn, online, maxPlayers uint32, version string, protocol int) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)

	// handshake
	length, err := readMCVarInt(reader)
	if err != nil || length < 1 || length > 1024 {
		return
	}
	handshake := make([]byte, length)
	if _, err := io.ReadFull(reader, handshake); err != nil {
		return
	}

	// status request
	requestLength, err := readMCVarInt(reader)
	if err != nil || requestLength < 1 || requestLength > 64 {
		return
	}
	request := make([]byte, requestLength)
	if _, err := io.ReadFull(reader, request); err != nil {
		return
	}

	payload := map[string]any{
		"version":     map[string]any{"name": version, "protocol": protocol},
		"players":     map[string]any{"online": online, "max": maxPlayers},
		"description": map[string]any{"text": "fake server"},
	}
	statusJSON, _ := json.Marshal(payload)
	var response bytes.Buffer
	writeMCVarInt(&response, 0x00) // packet id
	writeMCString(&response, string(statusJSON))
	var frame bytes.Buffer
	writeMCVarInt(&frame, response.Len())
	frame.Write(response.Bytes())
	if _, err := conn.Write(frame.Bytes()); err != nil {
		return
	}

	// ping/pong
	pingLength, err := readMCVarInt(reader)
	if err != nil || pingLength != 9 {
		return
	}
	pong := make([]byte, 9)
	if _, err := io.ReadFull(reader, pong); err != nil {
		return
	}
	if len(pong) > 0 && pong[0] == 0x01 {
		var pongFrame bytes.Buffer
		writeMCVarInt(&pongFrame, 9)
		pongFrame.Write(pong)
		_, _ = conn.Write(pongFrame.Bytes())
	}
}

// startFakeLegacyMCServer 启动一个只支持 legacy 0xFE 0x01 的假服务器。
func startFakeLegacyMCServer(t *testing.T, online, maxPlayers uint32, version string, protocol int) (uint16, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleFakeLegacyConnection(conn, online, maxPlayers, version, protocol)
		}
	}()
	return port, func() { _ = listener.Close() }
}

func handleFakeLegacyConnection(conn net.Conn, online, maxPlayers uint32, version string, protocol int) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	head := make([]byte, 2)
	if _, err := io.ReadFull(reader, head); err != nil {
		return
	}
	if head[0] != 0xFE {
		return
	}
	text := "\u00a71\x00" + strconv.Itoa(protocol) + "\x00" + version + "\x00fake motd\x00" +
		strconv.Itoa(int(online)) + "\x00" + strconv.Itoa(int(maxPlayers))
	units := utf16.Encode([]rune(text))
	var response bytes.Buffer
	response.WriteByte(0xFF)
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(units)))
	response.Write(length[:])
	for _, unit := range units {
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], unit)
		response.Write(encoded[:])
	}
	_, _ = conn.Write(response.Bytes())
}

// startFakePluginMCServer 启动一个只支持 legacy 0xFA MC|PingHost 插件消息的假服务器
// （模拟 1.4/1.5 服务器）：收到 0xFE 直接关闭连接，收到 0xFA 才回复。
func startFakePluginMCServer(t *testing.T, online, maxPlayers uint32) (uint16, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleFakePluginConnection(conn, online, maxPlayers)
		}
	}()
	return port, func() { _ = listener.Close() }
}

func handleFakePluginConnection(conn net.Conn, online, maxPlayers uint32) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	packetID, err := reader.ReadByte()
	if err != nil {
		return
	}
	if packetID != 0xFA {
		return // 只响应插件消息
	}
	// channel：2 字节大端长度 + 字符串
	channel, err := readFakeShortString(reader)
	if err != nil || channel != "MC|PingHost" {
		return
	}
	// data：2 字节大端长度 + 内容
	if _, err := readFakeShortString(reader); err != nil {
		return
	}
	text := "fake plugin motd\x00" + strconv.Itoa(int(online)) + "\x00" + strconv.Itoa(int(maxPlayers))
	writeFakeLegacyResponse(conn, text)
}

func readFakeShortString(reader *bufio.Reader) (string, error) {
	var length uint16
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return "", err
	}
	raw := make([]byte, int(length))
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", err
	}
	return string(raw), nil
}

func writeFakeLegacyResponse(conn net.Conn, text string) {
	units := utf16.Encode([]rune(text))
	var response bytes.Buffer
	response.WriteByte(0xFF)
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(units)))
	response.Write(length[:])
	for _, unit := range units {
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], unit)
		response.Write(encoded[:])
	}
	_, _ = conn.Write(response.Bytes())
}

// ---------------------------------------------------------------------------
// Ping 测试
// ---------------------------------------------------------------------------

func TestPingMCServerModern(t *testing.T) {
	port, closeFn := startFakeModernMCServer(t, 12, 100, "1.20.4", 765)
	defer closeFn()
	result, err := PingMCServer(context.Background(), "127.0.0.1", port)
	if err != nil {
		t.Fatalf("ping modern: %v", err)
	}
	if !result.Online {
		t.Fatal("expected online")
	}
	if result.OnlineCount != 12 || result.MaxPlayers != 100 {
		t.Fatalf("counts = %d/%d", result.OnlineCount, result.MaxPlayers)
	}
	if result.VersionName != "1.20.4" || result.Protocol != 765 {
		t.Fatalf("version = %q protocol = %d", result.VersionName, result.Protocol)
	}
	if result.LatencyMS < 0 {
		t.Fatalf("latency = %d", result.LatencyMS)
	}
}

func TestPingMCServerLegacyFallback(t *testing.T) {
	port, closeFn := startFakeLegacyMCServer(t, 5, 50, "1.8.9", 47)
	defer closeFn()
	result, err := PingMCServer(context.Background(), "127.0.0.1", port)
	if err != nil {
		t.Fatalf("ping legacy: %v", err)
	}
	if !result.Online {
		t.Fatal("expected online")
	}
	if result.OnlineCount != 5 || result.MaxPlayers != 50 {
		t.Fatalf("counts = %d/%d", result.OnlineCount, result.MaxPlayers)
	}
	if result.VersionName != "1.8.9" || result.Protocol != 47 {
		t.Fatalf("version = %q protocol = %d", result.VersionName, result.Protocol)
	}
	if result.Description != "fake motd" {
		t.Fatalf("motd = %q", result.Description)
	}
}

func TestPingMCServerLegacyPluginMessage(t *testing.T) {
	port, closeFn := startFakePluginMCServer(t, 7, 30)
	defer closeFn()
	result, err := PingMCServer(context.Background(), "127.0.0.1", port)
	if err != nil {
		t.Fatalf("ping plugin message: %v", err)
	}
	if !result.Online {
		t.Fatal("expected online")
	}
	if result.OnlineCount != 7 || result.MaxPlayers != 30 {
		t.Fatalf("counts = %d/%d", result.OnlineCount, result.MaxPlayers)
	}
	if result.Description != "fake plugin motd" {
		t.Fatalf("motd = %q", result.Description)
	}
	if result.VersionName != "Legacy" {
		t.Fatalf("version = %q", result.VersionName)
	}
}

func TestPingMCServerUnreachable(t *testing.T) {
	// 先监听拿到一个空闲端口，再立即关闭，确保端口不可达
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	_ = listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if _, err := PingMCServer(ctx, "127.0.0.1", port); err == nil {
		t.Fatal("expected ping to fail for unreachable address")
	} else if !strings.Contains(err.Error(), "ping 失败") {
		t.Fatalf("unexpected error: %v", err)
	}
}
