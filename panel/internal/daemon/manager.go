package daemon

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
)

type ConnectionDefinition struct {
	PanelNodeID string
	BaseURL     string
	Token       string
	Enabled     bool
}

func (m *Manager) NodeIDs() []string {
	m.mu.RLock()
	ids := make([]string, 0, len(m.connections))
	for id := range m.connections {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

type managedConnection struct {
	client *Client
	cancel context.CancelFunc
}

type Manager struct {
	logger   *slog.Logger
	onStatus func(string, RuntimeStatus)

	mu          sync.RWMutex
	ctx         context.Context
	connections map[string]managedConnection
	statuses    map[string]RuntimeStatus
}

func NewManager(logger *slog.Logger, onStatus func(string, RuntimeStatus)) *Manager {
	return &Manager{
		logger: logger, onStatus: onStatus,
		connections: make(map[string]managedConnection),
		statuses:    make(map[string]RuntimeStatus),
	}
}

func (m *Manager) Start(ctx context.Context, definitions []ConnectionDefinition) {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	for _, definition := range definitions {
		m.Upsert(definition)
	}
}

func (m *Manager) Upsert(definition ConnectionDefinition) {
	m.Remove(definition.PanelNodeID)
	m.mu.Lock()
	if !definition.Enabled {
		m.statuses[definition.PanelNodeID] = RuntimeStatus{State: "DISABLED", Capabilities: []string{}}
		m.mu.Unlock()
		return
	}
	if m.ctx == nil {
		m.statuses[definition.PanelNodeID] = RuntimeStatus{State: "OFFLINE", LastError: "connection manager is not started", Capabilities: []string{}}
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	client := NewClient(definition.BaseURL, definition.Token, m.logger)
	client.SetStatusCallback(func(status RuntimeStatus) {
		m.mu.Lock()
		m.statuses[definition.PanelNodeID] = status
		m.mu.Unlock()
		if m.onStatus != nil {
			m.onStatus(definition.PanelNodeID, status)
		}
	})
	m.connections[definition.PanelNodeID] = managedConnection{client: client, cancel: cancel}
	m.statuses[definition.PanelNodeID] = RuntimeStatus{State: "CONNECTING", Capabilities: []string{}}
	m.mu.Unlock()
	go client.Run(ctx)
}

func (m *Manager) Remove(panelNodeID string) {
	m.mu.Lock()
	connection, exists := m.connections[panelNodeID]
	if exists {
		delete(m.connections, panelNodeID)
	}
	delete(m.statuses, panelNodeID)
	m.mu.Unlock()
	if exists {
		connection.cancel()
	}
}

func (m *Manager) Status(panelNodeID string) RuntimeStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status, exists := m.statuses[panelNodeID]
	if !exists {
		return RuntimeStatus{State: "OFFLINE", Capabilities: []string{}}
	}
	return status
}

func (m *Manager) Call(ctx context.Context, panelNodeID, messageType string, input, output any) error {
	m.mu.RLock()
	connection, exists := m.connections[panelNodeID]
	m.mu.RUnlock()
	if !exists {
		return ErrDisconnected
	}
	return connection.client.Call(ctx, messageType, input, output)
}

func (m *Manager) ConsoleURL(panelNodeID string) (string, error) {
	m.mu.RLock()
	connection, exists := m.connections[panelNodeID]
	m.mu.RUnlock()
	if !exists {
		return "", ErrDisconnected
	}
	return connection.client.ConsoleURL()
}

func (m *Manager) UploadPlugin(ctx context.Context, panelNodeID, ticket, serverID, path string, output any) error {
	m.mu.RLock()
	connection, exists := m.connections[panelNodeID]
	m.mu.RUnlock()
	if !exists {
		return ErrDisconnected
	}
	return connection.client.UploadPlugin(ctx, ticket, serverID, path, output)
}

func (m *Manager) Close() {
	m.mu.Lock()
	connections := m.connections
	m.connections = make(map[string]managedConnection)
	m.statuses = make(map[string]RuntimeStatus)
	m.mu.Unlock()
	for _, connection := range connections {
		connection.cancel()
	}
}

func IsAuthenticationError(err error) bool {
	var apiError *APIError
	return errors.As(err, &apiError) && apiError.Code == "UNAUTHENTICATED"
}
