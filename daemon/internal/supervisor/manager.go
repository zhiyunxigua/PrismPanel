package supervisor

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
	"PrismPanel-daemon/internal/protocol"
)

type Manager struct {
	config        config.Config
	events        *eventbus.Bus
	mu            sync.RWMutex
	instances     map[string]*instance
	closing       atomic.Bool
	hostMu        sync.RWMutex
	host          HostSnapshot
	hostOnce      sync.Once
	beforeStartMu sync.RWMutex
	beforeStart   func(instanceID, workspace string) error
}

func (m *Manager) SetBeforeStart(hook func(instanceID, workspace string) error) {
	m.beforeStartMu.Lock()
	m.beforeStart = hook
	m.beforeStartMu.Unlock()
}

func NewManager(cfg config.Config, events *eventbus.Bus, servers []model.ServerConfig) (*Manager, error) {
	manager := &Manager{
		config: cfg, events: events, instances: make(map[string]*instance),
	}
	for _, server := range servers {
		if err := manager.validateServerLocked(server, ""); err != nil {
			return nil, fmt.Errorf("validate server %s: %w", server.ServerID, err)
		}
		for _, instanceConfig := range server.Instances() {
			manager.instances[instanceConfig.InstanceID] = newInstance(instanceConfig, cfg.Process.ConsoleBufferLines)
		}
	}
	return manager, nil
}

func (m *Manager) ValidateServer(server model.ServerConfig, replacingServerID string) error {
	server.Normalize()
	if err := server.Validate(); err != nil {
		return apperr.Wrap("INVALID_CONFIG", "服务器配置无效", err)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.validateServerLocked(server, replacingServerID)
}

func (m *Manager) validateServerLocked(server model.ServerConfig, replacingServerID string) error {
	for _, candidate := range server.Instances() {
		if candidate.Port == m.config.Server.Port {
			return apperr.New("PORT_CONFLICT", "实例端口与守护进程端口冲突")
		}
		for id, current := range m.instances {
			current.mu.RLock()
			currentConfig := current.cfg
			current.mu.RUnlock()
			if currentConfig.ServerID == replacingServerID {
				continue
			}
			if id == candidate.InstanceID {
				return apperr.New("INVALID_CONFIG", "实例 ID 与现有实例冲突")
			}
			if currentConfig.Port == candidate.Port {
				return apperr.New("PORT_CONFLICT", "实例端口与现有实例冲突")
			}
		}
	}
	instances := server.Instances()
	for left := range instances {
		for right := left + 1; right < len(instances); right++ {
			if instances[left].Port == instances[right].Port {
				return apperr.New("PORT_CONFLICT", "同一服务器内存在重复端口")
			}
		}
	}
	return nil
}

func (m *Manager) ApplyServer(server model.ServerConfig) ([]string, error) {
	server.Normalize()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateServerLocked(server, server.ServerID); err != nil {
		return nil, err
	}

	targets := make(map[string]model.InstanceConfig)
	for _, cfg := range server.Instances() {
		targets[cfg.InstanceID] = cfg
	}
	locked := make([]*instance, 0)
	for _, current := range m.instances {
		current.mu.RLock()
		sameServer := current.cfg.ServerID == server.ServerID
		current.mu.RUnlock()
		if sameServer {
			if !current.op.TryLock() {
				for _, item := range locked {
					item.op.Unlock()
				}
				return nil, apperr.New("INSTANCE_BUSY", "实例正在执行其他操作")
			}
			locked = append(locked, current)
			if err := deploymentLockError(current); err != nil {
				return nil, err
			}
		}
	}
	defer func() {
		for _, item := range locked {
			item.op.Unlock()
		}
	}()

	warnings := make([]string, 0)
	for id, cfg := range targets {
		current, exists := m.instances[id]
		if !exists {
			current = newInstance(cfg, m.config.Process.ConsoleBufferLines)
			m.instances[id] = current
		} else {
			current.mu.Lock()
			current.cfg = cfg
			current.managed = true
			current.mu.Unlock()
		}
		if info, err := os.Stat(cfg.Workspace); err == nil && info.IsDir() {
			if err := writeServerPort(cfg.Workspace, cfg.Port); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", id, err))
			}
		}
	}
	for id, current := range m.instances {
		current.mu.RLock()
		serverID := current.cfg.ServerID
		current.mu.RUnlock()
		if serverID == server.ServerID {
			if _, exists := targets[id]; !exists {
				current.mu.Lock()
				if current.state == StateStopped {
					current.mu.Unlock()
					delete(m.instances, id)
				} else {
					current.managed = false
					current.mu.Unlock()
				}
			}
		}
	}
	return warnings, nil
}

func (m *Manager) RemoveServer(serverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.instances {
		current.mu.RLock()
		belongs := current.cfg.ServerID == serverID
		state := current.state
		deploymentLocked := current.deploymentLocked
		current.mu.RUnlock()
		if belongs && (state != StateStopped || deploymentLocked) {
			return apperr.New("INSTANCE_BUSY", "服务器仍有实例未停止")
		}
	}
	for id, current := range m.instances {
		current.mu.RLock()
		belongs := current.cfg.ServerID == serverID
		current.mu.RUnlock()
		if belongs {
			delete(m.instances, id)
		}
	}
	return nil
}

func (m *Manager) List() []Snapshot {
	m.mu.RLock()
	instances := make([]*instance, 0, len(m.instances))
	for _, current := range m.instances {
		instances = append(instances, current)
	}
	m.mu.RUnlock()
	result := make([]Snapshot, 0, len(instances))
	for _, current := range instances {
		current.mu.RLock()
		managed := current.managed
		current.mu.RUnlock()
		if managed {
			result = append(result, current.snapshot())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].InstanceID < result[j].InstanceID })
	return result
}

func (m *Manager) Get(instanceID string) (Snapshot, error) {
	current, err := m.lookup(instanceID)
	if err != nil {
		return Snapshot{}, err
	}
	return current.snapshot(), nil
}

// WithFileMutation serializes a file write with lifecycle and deployment operations.
func (m *Manager) WithFileMutation(instanceID string, mutate func(workspace string) error) error {
	return m.withFileMutation(instanceID, false, mutate)
}

// WithStoppedFileMutation also requires the instance process to be inactive.
func (m *Manager) WithStoppedFileMutation(instanceID string, mutate func(workspace string) error) error {
	return m.withFileMutation(instanceID, true, mutate)
}

func (m *Manager) withFileMutation(instanceID string, requireStopped bool, mutate func(workspace string) error) error {
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	if !current.op.TryLock() {
		return apperr.New("INSTANCE_BUSY", "实例正在执行其他操作")
	}
	defer current.op.Unlock()
	if err := deploymentLockError(current); err != nil {
		return err
	}
	current.mu.RLock()
	workspace := current.cfg.Workspace
	state := current.state
	current.mu.RUnlock()
	if requireStopped && state != StateStopped && state != StateFailed {
		return apperr.New("INVALID_STATE", "实例运行时不能切换工作目录")
	}
	return mutate(workspace)
}

func (m *Manager) Subscribe(instanceID string, afterSequence uint64) ([]ConsoleLine, <-chan ConsoleLine, func(), error) {
	current, err := m.lookup(instanceID)
	if err != nil {
		return nil, nil, nil, err
	}
	current.mu.Lock()
	history := current.console.after(afterSequence)
	current.nextSubID++
	subscriptionID := current.nextSubID
	channel := make(chan ConsoleLine, 256)
	current.subscribers[subscriptionID] = channel
	current.mu.Unlock()
	cancel := func() {
		current.mu.Lock()
		if existing, ok := current.subscribers[subscriptionID]; ok {
			delete(current.subscribers, subscriptionID)
			close(existing)
		}
		current.mu.Unlock()
	}
	return history, channel, cancel, nil
}

func (m *Manager) lookup(instanceID string) (*instance, error) {
	m.mu.RLock()
	current := m.instances[instanceID]
	m.mu.RUnlock()
	if current == nil {
		return nil, apperr.New("INSTANCE_NOT_FOUND", "实例不存在")
	}
	current.mu.RLock()
	managed := current.managed
	current.mu.RUnlock()
	if !managed {
		return nil, apperr.New("INSTANCE_NOT_FOUND", "实例不在当前管理范围内")
	}
	return current, nil
}

func (m *Manager) publishState(current *instance) {
	m.events.Publish(protocol.Event("instance.state_changed", current.snapshot()))
}

func (m *Manager) SnapshotEvent() protocol.Outgoing {
	return protocol.Event("node.snapshot", map[string]any{
		"instances": m.List(),
	})
}
