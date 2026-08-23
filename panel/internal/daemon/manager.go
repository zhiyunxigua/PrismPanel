package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
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
	logger          *slog.Logger
	statusCallbacks []func(string, RuntimeStatus)
	onEvent         func(string, string, json.RawMessage)

	mu          sync.RWMutex
	ctx         context.Context
	connections map[string]managedConnection
	statuses    map[string]RuntimeStatus
}

func (m *Manager) SetEventCallback(callback func(string, string, json.RawMessage)) {
	m.mu.Lock()
	m.onEvent = callback
	m.mu.Unlock()
}

func (m *Manager) AddStatusCallback(callback func(string, RuntimeStatus)) {
	if callback == nil {
		return
	}
	m.mu.Lock()
	m.statusCallbacks = append(m.statusCallbacks, callback)
	m.mu.Unlock()
}

func NewManager(logger *slog.Logger, onStatus func(string, RuntimeStatus)) *Manager {
	manager := &Manager{
		logger:      logger,
		connections: make(map[string]managedConnection),
		statuses:    make(map[string]RuntimeStatus),
	}
	manager.AddStatusCallback(onStatus)
	return manager
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
		callbacks := append([]func(string, RuntimeStatus){}, m.statusCallbacks...)
		m.mu.Unlock()
		for _, callback := range callbacks {
			callback(definition.PanelNodeID, status)
		}
	})
	client.SetEventCallback(func(eventType string, data json.RawMessage) {
		m.mu.RLock()
		callback := m.onEvent
		m.mu.RUnlock()
		if callback != nil {
			callback(definition.PanelNodeID, eventType, data)
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

func (m *Manager) UploadPluginConfig(ctx context.Context, panelNodeID, ticket, serverID, path string, output any) error {
	m.mu.RLock()
	connection, exists := m.connections[panelNodeID]
	m.mu.RUnlock()
	if !exists {
		return ErrDisconnected
	}
	return connection.client.UploadPluginConfig(ctx, ticket, serverID, path, output)
}

func (m *Manager) UploadPluginContent(ctx context.Context, panelNodeID, ticket, serverID, path string, backupSnapshot bool, output any) error {
	m.mu.RLock()
	connection, exists := m.connections[panelNodeID]
	m.mu.RUnlock()
	if !exists {
		return ErrDisconnected
	}
	return connection.client.UploadPluginContent(ctx, ticket, serverID, path, backupSnapshot, output)
}

func (m *Manager) UploadInstancePlugin(
	ctx context.Context,
	panelNodeID string,
	ticket string,
	instanceID string,
	filename string,
	overwrite bool,
	body io.Reader,
	contentLength int64,
	output any,
) error {
	m.mu.RLock()
	connection, exists := m.connections[panelNodeID]
	m.mu.RUnlock()
	if !exists {
		return ErrDisconnected
	}
	return connection.client.UploadInstancePlugin(
		ctx, ticket, instanceID, filename, overwrite, body, contentLength, output,
	)
}

func (m *Manager) FileRequest(ctx context.Context, panelNodeID, operation, method string, headers http.Header, body io.Reader, contentLength int64) (*http.Response, error) {
	m.mu.RLock()
	connection, exists := m.connections[panelNodeID]
	m.mu.RUnlock()
	if !exists {
		return nil, ErrDisconnected
	}
	return connection.client.FileRequest(ctx, operation, method, headers, body, contentLength)
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
