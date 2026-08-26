package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var ErrDisconnected = errors.New("daemon is disconnected")

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type envelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Token     string          `json:"token,omitempty"`
	Success   *bool           `json:"success,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     *APIError       `json:"error,omitempty"`
}

type result struct {
	data json.RawMessage
	err  error
}

type Client struct {
	baseURL string
	secret  string
	logger  *slog.Logger

	mu              sync.RWMutex
	conn            *websocket.Conn
	connected       bool
	publicURL       string
	version         string
	nodeID          string
	protocolVersion string
	capabilities    []string
	latencyMS       int64
	connectedAt     time.Time
	lastError       string
	onStatus        func(RuntimeStatus)
	onEvent         func(string, json.RawMessage)
	writeMu         sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan result
}

func NewClient(baseURL, secret string, logger *slog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), secret: secret,
		logger: logger, pending: make(map[string]chan result),
	}
}

type Metadata struct {
	NodeID          string   `json:"node_id"`
	Version         string   `json:"version"`
	ProtocolVersion string   `json:"protocol_version"`
	PublicURL       string   `json:"public_url"`
	Capabilities    []string `json:"capabilities"`
}

type RuntimeStatus struct {
	State           string    `json:"status"`
	NodeID          string    `json:"daemon_id,omitempty"`
	Version         string    `json:"daemon_version,omitempty"`
	ProtocolVersion string    `json:"protocol_version,omitempty"`
	PublicURL       string    `json:"reported_public_url,omitempty"`
	Capabilities    []string  `json:"capabilities"`
	LatencyMS       int64     `json:"latency_ms"`
	ConnectedAt     time.Time `json:"last_connected_at,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

func (c *Client) SetStatusCallback(callback func(RuntimeStatus)) {
	c.mu.Lock()
	c.onStatus = callback
	c.mu.Unlock()
}

func (c *Client) SetEventCallback(callback func(string, json.RawMessage)) {
	c.mu.Lock()
	c.onEvent = callback
	c.mu.Unlock()
}

func (c *Client) Run(ctx context.Context) {
	go func() {
		<-ctx.Done()
		c.disconnect(nil)
	}()
	retryDelay := time.Second
	for {
		if ctx.Err() != nil {
			c.disconnect(nil)
			return
		}
		err := c.connect(ctx)
		if ctx.Err() != nil {
			c.disconnect(nil)
			return
		}
		c.mu.Lock()
		c.lastError = err.Error()
		callback := c.onStatus
		status := c.runtimeStatusLocked()
		c.mu.Unlock()
		if callback != nil {
			callback(status)
		}
		c.logger.Warn("daemon connection lost", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
		if retryDelay < 30*time.Second {
			retryDelay *= 2
			if retryDelay > 30*time.Second {
				retryDelay = 30 * time.Second
			}
		}
	}
}

func (c *Client) connect(ctx context.Context) error {
	controlURL, err := websocketEndpoint(c.baseURL, "/api/v1/ws/control")
	if err != nil {
		return err
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, controlURL, nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return err
	}
	startedAt := time.Now()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteJSON(envelope{Type: "auth", Token: c.secret}); err != nil {
		conn.Close()
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var auth envelope
	if err := conn.ReadJSON(&auth); err != nil {
		conn.Close()
		return err
	}
	if auth.Type != "auth.result" || auth.Success == nil || !*auth.Success {
		conn.Close()
		if auth.Error != nil {
			return auth.Error
		}
		return errors.New("daemon authentication failed")
	}
	var metadata Metadata
	_ = json.Unmarshal(auth.Data, &metadata)
	_ = conn.SetReadDeadline(time.Time{})
	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.publicURL = metadata.PublicURL
	c.version = metadata.Version
	c.nodeID = metadata.NodeID
	c.protocolVersion = metadata.ProtocolVersion
	c.capabilities = metadata.Capabilities
	c.latencyMS = time.Since(startedAt).Milliseconds()
	c.connectedAt = time.Now().UTC()
	c.lastError = ""
	callback := c.onStatus
	status := c.runtimeStatusLocked()
	c.mu.Unlock()
	if callback != nil {
		callback(status)
	}
	c.logger.Info("connected to daemon", "url", c.baseURL, "version", metadata.Version)
	err = c.readLoop(conn)
	c.disconnect(conn)
	return err
}

func (c *Client) readLoop(conn *websocket.Conn) error {
	for {
		var message envelope
		if err := conn.ReadJSON(&message); err != nil {
			return err
		}
		if message.Type != "response" || message.RequestID == "" {
			c.mu.RLock()
			callback := c.onEvent
			c.mu.RUnlock()
			if callback != nil && message.Type != "" {
				data := append(json.RawMessage(nil), message.Data...)
				go callback(message.Type, data)
			}
			continue
		}
		c.pendingMu.Lock()
		waiter := c.pending[message.RequestID]
		delete(c.pending, message.RequestID)
		c.pendingMu.Unlock()
		if waiter == nil {
			continue
		}
		if message.Success != nil && *message.Success {
			waiter <- result{data: message.Data}
		} else if message.Error != nil {
			waiter <- result{err: message.Error}
		} else {
			waiter <- result{err: errors.New("daemon request failed")}
		}
	}
}

// FileRequest 向 daemon 的 /api/v1/files/<operation> 发起文件请求。
// query 中的参数会以 URL query 形式透传（如 path，需百分号编码以支持中文路径），
// headers 仅用于 ASCII 安全的自定义头。
func (c *Client) FileRequest(ctx context.Context, operation, method string, headers http.Header, query url.Values, body io.Reader, contentLength int64) (*http.Response, error) {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/files/" + strings.Trim(operation, "/")
	endpoint.RawQuery = ""
	queryValues := endpoint.Query()
	for key, values := range query {
		for _, value := range values {
			queryValues.Add(key, value)
		}
	}
	endpoint.RawQuery = queryValues.Encode()
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	request.ContentLength = contentLength
	request.Header = headers.Clone()
	return http.DefaultClient.Do(request)
}

func (c *Client) Call(ctx context.Context, messageType string, input any, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	requestID := randomID()
	waiter := make(chan result, 1)
	c.pendingMu.Lock()
	c.pending[requestID] = waiter
	c.pendingMu.Unlock()

	c.mu.RLock()
	conn := c.conn
	connected := c.connected
	c.mu.RUnlock()
	if !connected || conn == nil {
		c.removePending(requestID)
		return ErrDisconnected
	}
	c.writeMu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = conn.WriteJSON(envelope{Type: messageType, RequestID: requestID, Data: raw})
	c.writeMu.Unlock()
	if err != nil {
		c.removePending(requestID)
		return ErrDisconnected
	}
	select {
	case <-ctx.Done():
		c.removePending(requestID)
		return ctx.Err()
	case response := <-waiter:
		if response.err != nil {
			return response.err
		}
		if output == nil {
			return nil
		}
		// 请求方期望返回数据，但 daemon 返回了空 payload：必须显式报错，
		// 否则调用方（如 server.list）会拿到 nil 结果并静默当作成功，
		// 前端进而把「空列表」误判为「服务器不存在」。
		if len(response.data) == 0 {
			return errors.New("daemon returned an empty response")
		}
		return json.Unmarshal(response.data, output)
	}
}

func (c *Client) removePending(requestID string) {
	c.pendingMu.Lock()
	delete(c.pending, requestID)
	c.pendingMu.Unlock()
}

func (c *Client) disconnect(expected *websocket.Conn) {
	c.mu.Lock()
	if expected != nil && c.conn != expected {
		c.mu.Unlock()
		return
	}
	conn := c.conn
	c.conn = nil
	c.connected = false
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
	c.pendingMu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan result)
	c.pendingMu.Unlock()
	for _, waiter := range pending {
		waiter <- result{err: ErrDisconnected}
	}
}

func (c *Client) Status() (connected bool, publicURL, version string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected, c.publicURL, c.version
}

func (c *Client) RuntimeStatus() RuntimeStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeStatusLocked()
}

func (c *Client) runtimeStatusLocked() RuntimeStatus {
	state := "OFFLINE"
	if c.connected {
		state = "ONLINE"
	}
	return RuntimeStatus{
		State: state, NodeID: c.nodeID, Version: c.version,
		ProtocolVersion: c.protocolVersion, PublicURL: c.publicURL,
		Capabilities: append([]string(nil), c.capabilities...), LatencyMS: c.latencyMS,
		ConnectedAt: c.connectedAt, LastError: c.lastError,
	}
}

func Probe(ctx context.Context, baseURL, token string) (Metadata, int64, error) {
	client := NewClient(baseURL, token, slog.Default())
	startedAt := time.Now()
	controlURL, err := websocketEndpoint(client.baseURL, "/api/v1/ws/control")
	if err != nil {
		return Metadata{}, 0, err
	}
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, controlURL, nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return Metadata{}, 0, err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteJSON(envelope{Type: "auth", Token: token}); err != nil {
		return Metadata{}, 0, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var auth envelope
	if err := conn.ReadJSON(&auth); err != nil {
		return Metadata{}, 0, err
	}
	if auth.Type != "auth.result" || auth.Success == nil || !*auth.Success {
		if auth.Error != nil {
			return Metadata{}, 0, auth.Error
		}
		return Metadata{}, 0, errors.New("daemon authentication failed")
	}
	var metadata Metadata
	if err := json.Unmarshal(auth.Data, &metadata); err != nil {
		return Metadata{}, 0, err
	}
	if metadata.NodeID == "" {
		return Metadata{}, 0, errors.New("daemon did not provide a node id")
	}
	return metadata, time.Since(startedAt).Milliseconds(), nil
}

func (c *Client) ConsoleURL() (string, error) {
	return websocketEndpoint(c.baseURL, "/api/v1/ws/console")
}

func (c *Client) UploadPlugin(ctx context.Context, ticket, serverID, path string, output any) error {
	return c.uploadPluginBundle(ctx, ticket, serverID, path, "/api/v1/plugins/deploy", false, output)
}

func (c *Client) UploadPluginConfig(ctx context.Context, ticket, serverID, path string, output any) error {
	return c.uploadPluginBundle(ctx, ticket, serverID, path, "/api/v1/plugins/config/deploy", false, output)
}

// UploadPluginContent 上传通用内容包 bundle；backupSnapshot 为完全配置高风险标记，
// daemon 在部署前做整目录快照备份（经 query 参数传给 /api/v1/plugins/content/deploy）。
func (c *Client) UploadPluginContent(ctx context.Context, ticket, serverID, path string, backupSnapshot bool, output any) error {
	return c.uploadPluginBundle(ctx, ticket, serverID, path, "/api/v1/plugins/content/deploy", backupSnapshot, output)
}

func (c *Client) uploadPluginBundle(ctx context.Context, ticket, serverID, path, endpointPath string, backupSnapshot bool, output any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + endpointPath
	query := endpoint.Query()
	query.Set("server_id", serverID)
	if backupSnapshot {
		query.Set("backup_snapshot", "true")
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), file)
	if err != nil {
		return err
	}
	request.ContentLength = info.Size()
	request.Header.Set("Authorization", "Bearer "+ticket)
	request.Header.Set("Content-Type", "application/zip")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var payload struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *APIError       `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !payload.Success {
		if payload.Error != nil {
			return payload.Error
		}
		return errors.New("daemon plugin upload failed")
	}
	if output == nil || len(payload.Data) == 0 {
		return nil
	}
	return json.Unmarshal(payload.Data, output)
}

func (c *Client) UploadInstancePlugin(
	ctx context.Context,
	ticket string,
	instanceID string,
	filename string,
	overwrite bool,
	body io.Reader,
	contentLength int64,
	output any,
) error {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/plugins/upload"
	query := endpoint.Query()
	query.Set("instance_id", instanceID)
	query.Set("filename", filename)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return err
	}
	request.ContentLength = contentLength
	request.Header.Set("Authorization", "Bearer "+ticket)
	request.Header.Set("Content-Type", "application/java-archive")
	request.Header.Set("X-Prism-Overwrite", strconv.FormatBool(overwrite))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var payload struct {
		Success bool
		Data    json.RawMessage
		Error   *APIError
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if output != nil && len(payload.Data) > 0 {
		if err := json.Unmarshal(payload.Data, output); err != nil {
			return err
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !payload.Success {
		if payload.Error != nil {
			return payload.Error
		}
		return errors.New("daemon instance plugin upload failed")
	}
	return nil
}

// PendingList 查询实例（instanceID 为空时查询全部实例）的插件 pending 队列与失败侧写。
func (c *Client) PendingList(ctx context.Context, instanceID string, output any) error {
	return c.Call(ctx, "pending.list", map[string]any{"instance_id": instanceID}, output)
}

// PendingClear 清除实例的插件 pending 队列；index/failedIndex 均为 nil 时整队清除，
// 否则删除对应下标的单条（index 针对 pending 队列，failedIndex 针对失败侧写）。
func (c *Client) PendingClear(ctx context.Context, instanceID string, index, failedIndex *int, output any) error {
	return c.Call(ctx, "pending.clear", map[string]any{
		"instance_id": instanceID, "index": index, "failed_index": failedIndex,
	}, output)
}

func websocketEndpoint(base, path string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", errors.New("daemon URL scheme must be http or https")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func PublicConsoleURL(publicURL string) (string, error) {
	return websocketEndpoint(publicURL, "/api/v1/ws/console")
}

func randomID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
