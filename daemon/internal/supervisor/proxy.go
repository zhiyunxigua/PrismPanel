package supervisor

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
)

var backendIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type ProxyBackend struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

type ProxyBackendCatalog struct {
	Revision int64          `json:"revision"`
	Servers  []ProxyBackend `json:"servers"`
}

type ProxyBackendResult struct {
	Revision int64 `json:"revision"`
	Applied  int   `json:"applied"`
	Removed  int   `json:"removed"`
}

type ProxySyncStatus struct {
	InstanceID string    `json:"instance_id"`
	Revision   int64     `json:"revision"`
	State      string    `json:"state"`
	Applied    int       `json:"applied,omitempty"`
	Removed    int       `json:"removed,omitempty"`
	Error      string    `json:"error,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type PlayerTransferInput struct {
	InstanceID     string `json:"instance_id"`
	PlayerUUID     string `json:"player_uuid"`
	TargetServerID string `json:"target_server_id"`
}

func (m *Manager) SyncProxyBackends(
	ctx context.Context,
	instanceID string,
	catalog ProxyBackendCatalog,
) (ProxySyncStatus, error) {
	if err := validateBackendCatalog(catalog); err != nil {
		return ProxySyncStatus{}, err
	}
	current, err := m.lookup(instanceID)
	if err != nil {
		return ProxySyncStatus{}, err
	}
	current.mu.Lock()
	if !model.IsProxyPlatform(current.cfg.Platform) {
		current.mu.Unlock()
		return ProxySyncStatus{}, apperr.New("INVALID_STATE", "target instance is not a proxy server")
	}
	if current.proxyCatalog != nil {
		if catalog.Revision < current.proxyCatalog.Revision {
			current.mu.Unlock()
			return ProxySyncStatus{}, apperr.New("STALE_REVISION", "proxy backend revision is stale")
		}
		if catalog.Revision == current.proxyCatalog.Revision && !sameBackendCatalog(*current.proxyCatalog, catalog) {
			current.mu.Unlock()
			return ProxySyncStatus{}, apperr.New("REVISION_CONFLICT", "proxy backend revision was reused")
		}
	}
	copyCatalog := cloneBackendCatalog(catalog)
	current.proxyCatalog = &copyCatalog
	connection := current.pluginConnection
	status := ProxySyncStatus{
		InstanceID: instanceID,
		Revision:   catalog.Revision,
		State:      "pending",
		UpdatedAt:  time.Now().UTC(),
	}
	current.proxySync = status
	connected := current.pluginConnected && connection != nil
	current.mu.Unlock()
	m.publishState(current)
	if !connected {
		return status, nil
	}
	return m.applyProxyCatalog(ctx, current, connection, catalog)
}

func (m *Manager) RestoreProxyBackends(instanceID string) {
	current, err := m.lookup(instanceID)
	if err != nil {
		return
	}
	current.mu.RLock()
	connection := current.pluginConnection
	connected := current.pluginConnected && connection != nil
	var catalog *ProxyBackendCatalog
	if current.proxyCatalog != nil {
		copyCatalog := cloneBackendCatalog(*current.proxyCatalog)
		catalog = &copyCatalog
	}
	current.mu.RUnlock()
	if !connected || catalog == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, _ = m.applyProxyCatalog(ctx, current, connection, *catalog)
}

func (m *Manager) applyProxyCatalog(
	ctx context.Context,
	current *instance,
	connection *PluginConnection,
	catalog ProxyBackendCatalog,
) (ProxySyncStatus, error) {
	current.mu.Lock()
	current.proxySync.State = "syncing"
	current.proxySync.Error = ""
	current.proxySync.UpdatedAt = time.Now().UTC()
	current.mu.Unlock()
	m.publishState(current)
	var result ProxyBackendResult
	err := connection.Request(ctx, "proxy.backends.replace", catalog, &result)
	current.mu.Lock()
	status := current.proxySync
	if err != nil {
		status.State = "failed"
		status.Error = err.Error()
	} else {
		status.State = "synced"
		status.Applied = result.Applied
		status.Removed = result.Removed
		status.Error = ""
	}
	status.UpdatedAt = time.Now().UTC()
	current.proxySync = status
	current.mu.Unlock()
	m.publishState(current)
	return status, err
}

func (m *Manager) ProxySyncStatus(instanceID string) (ProxySyncStatus, error) {
	current, err := m.lookup(instanceID)
	if err != nil {
		return ProxySyncStatus{}, err
	}
	current.mu.RLock()
	defer current.mu.RUnlock()
	if !model.IsProxyPlatform(current.cfg.Platform) {
		return ProxySyncStatus{}, apperr.New("INVALID_STATE", "target instance is not a proxy server")
	}
	status := current.proxySync
	if status.State == "" {
		status = ProxySyncStatus{InstanceID: instanceID, State: "unconfigured"}
	}
	return status, nil
}

func (m *Manager) TransferPlayer(ctx context.Context, input PlayerTransferInput) error {
	current, err := m.lookup(input.InstanceID)
	if err != nil {
		return err
	}
	current.mu.RLock()
	connection := current.pluginConnection
	connected := current.pluginConnected && connection != nil
	current.mu.RUnlock()
	if !connected {
		return apperr.New("PLUGIN_DISCONNECTED", "proxy plugin is not connected")
	}
	return connection.Request(ctx, "player.transfer", map[string]string{
		"player_uuid":      input.PlayerUUID,
		"target_server_id": input.TargetServerID,
	}, nil)
}

func validateBackendCatalog(catalog ProxyBackendCatalog) error {
	if catalog.Revision < 0 {
		return apperr.New("INVALID_REQUEST", "revision must not be negative")
	}
	if len(catalog.Servers) > 10000 {
		return apperr.New("INVALID_REQUEST", "proxy backend catalog is too large")
	}
	seen := make(map[string]struct{}, len(catalog.Servers))
	for _, server := range catalog.Servers {
		if !backendIDPattern.MatchString(server.ID) {
			return apperr.New("INVALID_REQUEST", "proxy backend id is invalid")
		}
		if _, exists := seen[server.ID]; exists {
			return apperr.New("DUPLICATE_SERVER_ID", "proxy backend id is duplicated")
		}
		seen[server.ID] = struct{}{}
		host, portText, err := net.SplitHostPort(server.Address)
		if err != nil || host == "" {
			return apperr.New("INVALID_REQUEST", fmt.Sprintf("invalid proxy backend address: %s", server.Address))
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return apperr.New("INVALID_REQUEST", fmt.Sprintf("invalid proxy backend address: %s", server.Address))
		}
	}
	return nil
}

func cloneBackendCatalog(value ProxyBackendCatalog) ProxyBackendCatalog {
	servers := append([]ProxyBackend(nil), value.Servers...)
	sort.Slice(servers, func(i, j int) bool { return servers[i].ID < servers[j].ID })
	return ProxyBackendCatalog{Revision: value.Revision, Servers: servers}
}

func sameBackendCatalog(left, right ProxyBackendCatalog) bool {
	left = cloneBackendCatalog(left)
	right = cloneBackendCatalog(right)
	if left.Revision != right.Revision || len(left.Servers) != len(right.Servers) {
		return false
	}
	for index := range left.Servers {
		if left.Servers[index] != right.Servers[index] {
			return false
		}
	}
	return true
}
