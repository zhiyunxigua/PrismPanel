package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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
	Secret    string          `json:"secret,omitempty"`
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

	mu        sync.RWMutex
	conn      *websocket.Conn
	connected bool
	publicURL string
	version   string
	writeMu   sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan result
}

func NewClient(baseURL, secret string, logger *slog.Logger) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"), secret: secret,
		logger: logger, pending: make(map[string]chan result),
	}
}

func (c *Client) Run(ctx context.Context) {
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
		c.logger.Warn("daemon connection lost", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
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
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteJSON(envelope{Type: "auth", Secret: c.secret}); err != nil {
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
	var metadata struct {
		PublicURL string `json:"public_url"`
		Version   string `json:"version"`
	}
	_ = json.Unmarshal(auth.Data, &metadata)
	_ = conn.SetReadDeadline(time.Time{})
	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.publicURL = metadata.PublicURL
	c.version = metadata.Version
	c.mu.Unlock()
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
		if output == nil || len(response.data) == 0 {
			return nil
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

func (c *Client) ConsoleURL() (string, error) {
	return websocketEndpoint(c.baseURL, "/api/v1/ws/console")
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
