package supervisor

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
)

type PluginConnection struct {
	manager      *Manager
	current      *instance
	sessionID    string
	generation   uint64
	platform     string
	capabilities map[string]struct{}
	outgoing     chan PluginRequest
	done         chan struct{}
	closeOnce    sync.Once
	pendingMu    sync.Mutex
	pending      map[string]chan PluginResponse
	sequence     atomic.Uint64
}

type PluginRequest struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Data      any    `json:"data,omitempty"`
}

type PluginResponse struct {
	RequestID string          `json:"request_id"`
	Success   bool            `json:"success"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     *apperr.Error   `json:"error,omitempty"`
}

func (m *Manager) RegisterPlugin(
	instanceID, sessionID, token string,
	pid int,
	platform string,
	capabilities []string,
) (*PluginConnection, error) {
	current, err := m.lookup(instanceID)
	if err != nil {
		return nil, apperr.New("UNAUTHENTICATED", "插件实例凭据无效")
	}
	candidate := sha256.Sum256([]byte(token))
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.state != StateRunning || current.pid == 0 {
		return nil, apperr.New("UNAUTHENTICATED", "插件实例凭据无效")
	}
	if current.sessionID != sessionID || !processBelongsToTree(pid, current.pid) || !current.pluginTokenSet ||
		subtle.ConstantTimeCompare(candidate[:], current.pluginTokenHash[:]) != 1 {
		return nil, apperr.New("UNAUTHENTICATED", "插件实例凭据无效")
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform != model.PluginTypeForPlatform(current.cfg.Platform) {
		return nil, apperr.New("UNAUTHENTICATED", "plugin platform does not match the instance")
	}
	declared := normalizeCapabilities(capabilities)
	current.pluginGeneration++
	current.pluginConnected = true
	current.pluginCapabilities = capabilityNames(declared)
	now := time.Now().UTC()
	current.pluginLastSeen = &now
	connection := &PluginConnection{
		manager: m, current: current, sessionID: sessionID, generation: current.pluginGeneration,
		platform: platform, capabilities: declared,
		outgoing: make(chan PluginRequest, 64), done: make(chan struct{}),
		pending: make(map[string]chan PluginResponse),
	}
	current.pluginConnection = connection
	return connection, nil
}

func (c *PluginConnection) Heartbeat() error {
	current := c.current
	current.mu.Lock()
	if current.sessionID != c.sessionID || current.pluginGeneration != c.generation || !current.pluginConnected {
		current.mu.Unlock()
		return apperr.New("UNAUTHENTICATED", "插件连接已失效")
	}
	now := time.Now().UTC()
	current.pluginLastSeen = &now
	current.mu.Unlock()
	return nil
}

func (c *PluginConnection) Update(report PluginReport) error {
	if report.OnlinePlayers < 0 || report.MaxPlayers < 0 || report.OnlinePlayers > report.MaxPlayers ||
		(report.TPS != nil && *report.TPS < 0) || (report.MSPT != nil && *report.MSPT < 0) ||
		report.JVMThreads < 0 ||
		len(report.Players) > 10000 || len(report.Plugins) > 5000 {
		return apperr.New("INVALID_REQUEST", "插件状态数据无效")
	}
	report.Players = append([]PlayerSnapshot(nil), report.Players...)
	report.Plugins = append([]LoadedPlugin(nil), report.Plugins...)
	current := c.current
	current.mu.Lock()
	if current.sessionID != c.sessionID || current.pluginGeneration != c.generation || !current.pluginConnected {
		current.mu.Unlock()
		return apperr.New("UNAUTHENTICATED", "插件连接已失效")
	}
	current.pluginReport = report
	now := time.Now().UTC()
	current.pluginLastSeen = &now
	current.mu.Unlock()
	c.manager.publishState(current)
	return nil
}

func (c *PluginConnection) Close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.pendingMu.Lock()
		c.pending = make(map[string]chan PluginResponse)
		c.pendingMu.Unlock()
	})
	current := c.current
	current.mu.Lock()
	if current.sessionID == c.sessionID && current.pluginGeneration == c.generation {
		current.pluginConnected = false
		current.pluginCapabilities = nil
		current.pluginConnection = nil
	}
	current.mu.Unlock()
	c.manager.publishState(current)
}

func normalizeCapabilities(values []string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func capabilityNames(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (c *PluginConnection) Outgoing() <-chan PluginRequest {
	return c.outgoing
}

func (c *PluginConnection) Done() <-chan struct{} {
	return c.done
}

func (c *PluginConnection) HandleResponse(response PluginResponse) {
	c.pendingMu.Lock()
	waiter := c.pending[response.RequestID]
	if waiter != nil {
		delete(c.pending, response.RequestID)
	}
	c.pendingMu.Unlock()
	if waiter != nil {
		waiter <- response
	}
}

func (c *PluginConnection) Request(ctx context.Context, messageType string, data any, result any) error {
	if !c.HasCapability(capabilityForCommand(messageType)) {
		return apperr.New("PLUGIN_CAPABILITY_MISSING", "plugin does not support the requested command")
	}
	requestID := fmt.Sprintf("plugin-%d", c.sequence.Add(1))
	waiter := make(chan PluginResponse, 1)
	c.pendingMu.Lock()
	c.pending[requestID] = waiter
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, requestID)
		c.pendingMu.Unlock()
	}()
	select {
	case c.outgoing <- PluginRequest{Type: messageType, RequestID: requestID, Data: data}:
	case <-c.done:
		return apperr.New("PLUGIN_DISCONNECTED", "plugin connection is unavailable")
	case <-ctx.Done():
		return apperr.Wrap("PLUGIN_TIMEOUT", "plugin command timed out", ctx.Err())
	}
	select {
	case response := <-waiter:
		if !response.Success {
			if response.Error != nil {
				return response.Error
			}
			return apperr.New("PLUGIN_COMMAND_FAILED", "plugin command failed")
		}
		if result != nil && len(response.Data) > 0 {
			if err := json.Unmarshal(response.Data, result); err != nil {
				return apperr.Wrap("INVALID_PLUGIN_RESPONSE", "plugin response is invalid", err)
			}
		}
		return nil
	case <-c.done:
		return apperr.New("PLUGIN_DISCONNECTED", "plugin connection is unavailable")
	case <-ctx.Done():
		return apperr.Wrap("PLUGIN_TIMEOUT", "plugin command timed out", ctx.Err())
	}
}

func (c *PluginConnection) HasCapability(capability string) bool {
	if capability == "" {
		return true
	}
	_, exists := c.capabilities[capability]
	return exists
}

func capabilityForCommand(messageType string) string {
	switch messageType {
	case "proxy.backends.replace":
		return "proxy.backends"
	case "player.transfer":
		return "player.transfer"
	default:
		return ""
	}
}

func (m *Manager) SetPluginPendingRestart(instanceID string, pending bool) {
	current, err := m.lookup(instanceID)
	if err != nil {
		return
	}
	current.mu.Lock()
	changed := current.pluginPendingRestart != pending
	current.pluginPendingRestart = pending
	current.mu.Unlock()
	if changed {
		m.publishState(current)
	}
}
