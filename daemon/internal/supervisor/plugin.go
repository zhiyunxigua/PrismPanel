package supervisor

import (
	"crypto/sha256"
	"crypto/subtle"
	"time"

	"PrismPanel-daemon/internal/apperr"
)

type PluginConnection struct {
	manager    *Manager
	current    *instance
	sessionID  string
	generation uint64
}

func (m *Manager) RegisterPlugin(instanceID, sessionID, token string, pid int) (*PluginConnection, error) {
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
	current.pluginGeneration++
	current.pluginConnected = true
	now := time.Now().UTC()
	current.pluginLastSeen = &now
	return &PluginConnection{
		manager: m, current: current, sessionID: sessionID, generation: current.pluginGeneration,
	}, nil
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
	current := c.current
	current.mu.Lock()
	if current.sessionID == c.sessionID && current.pluginGeneration == c.generation {
		current.pluginConnected = false
	}
	current.mu.Unlock()
	c.manager.publishState(current)
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
