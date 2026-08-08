package supervisor

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
	"PrismPanel-daemon/internal/protocol"
)

type OperatorEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type OperatorSource struct {
	PanelID   string          `json:"panel_id"`
	Revision  uint64          `json:"revision"`
	Operators []OperatorEntry `json:"operators"`
}

type OperatorRegistryState struct {
	Revision uint64           `json:"revision"`
	Sources  []OperatorSource `json:"sources"`
}

type OperatorCatalog struct {
	Revision  uint64          `json:"revision"`
	Active    bool            `json:"active"`
	Operators []OperatorEntry `json:"operators"`
}

type OperatorApplyResult struct {
	Revision uint64 `json:"revision"`
	Applied  int    `json:"applied"`
	Removed  int    `json:"removed"`
}

type OperatorSyncStatus struct {
	InstanceID string    `json:"instance_id"`
	Revision   uint64    `json:"revision"`
	State      string    `json:"state"`
	Applied    int       `json:"applied,omitempty"`
	Removed    int       `json:"removed,omitempty"`
	Error      string    `json:"error,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type OperatorRegistryStatus struct {
	PanelID           string               `json:"panel_id"`
	SourcePresent     bool                 `json:"source_present"`
	SourceRevision    uint64               `json:"source_revision"`
	EffectiveRevision uint64               `json:"effective_revision"`
	Active            bool                 `json:"active"`
	Targets           []OperatorSyncStatus `json:"targets"`
}

type OperatorDriftReport struct {
	Revision uint64   `json:"revision"`
	Restored []string `json:"restored,omitempty"`
	Removed  []string `json:"removed,omitempty"`
}

func (m *Manager) ConfigureOperators(
	state OperatorRegistryState,
	save func(OperatorRegistryState) error,
) error {
	normalized, err := normalizeOperatorState(state)
	if err != nil {
		return err
	}
	m.operatorMu.Lock()
	m.operators = normalized
	m.saveOperators = save
	m.operatorMu.Unlock()
	return nil
}

func (m *Manager) ReplaceOperatorSource(
	ctx context.Context,
	source OperatorSource,
) (OperatorRegistryStatus, error) {
	normalized, err := normalizeOperatorSource(source)
	if err != nil {
		return OperatorRegistryStatus{}, err
	}
	m.operatorMu.Lock()
	state := cloneOperatorState(m.operators)
	index := operatorSourceIndex(state.Sources, normalized.PanelID)
	if index >= 0 {
		current := state.Sources[index]
		if normalized.Revision < current.Revision {
			m.operatorMu.Unlock()
			return OperatorRegistryStatus{}, apperr.New("STALE_REVISION", "operator source revision is stale")
		}
		if normalized.Revision == current.Revision {
			if !sameOperatorSource(current, normalized) {
				m.operatorMu.Unlock()
				return OperatorRegistryStatus{}, apperr.New("REVISION_CONFLICT", "operator source revision was reused")
			}
			status := m.operatorStatusLocked(normalized.PanelID)
			m.operatorMu.Unlock()
			return status, nil
		}
		state.Sources[index] = normalized
	} else {
		state.Sources = append(state.Sources, normalized)
	}
	state.Revision++
	sortOperatorSources(state.Sources)
	if err := m.persistOperatorsLocked(state); err != nil {
		m.operatorMu.Unlock()
		return OperatorRegistryStatus{}, apperr.Wrap("CONFIG_WRITE_FAILED", "operator registry could not be saved", err)
	}
	m.operators = state
	catalog := operatorCatalog(state)
	m.operatorMu.Unlock()
	m.applyOperatorCatalog(ctx, catalog)
	return m.OperatorStatus(normalized.PanelID), nil
}

func (m *Manager) RemoveOperatorSource(
	ctx context.Context,
	panelID string,
) (OperatorRegistryStatus, error) {
	panelID = strings.ToLower(strings.TrimSpace(panelID))
	if !validPanelID(panelID) {
		return OperatorRegistryStatus{}, apperr.New("INVALID_REQUEST", "panel_id is invalid")
	}
	m.operatorMu.Lock()
	state := cloneOperatorState(m.operators)
	index := operatorSourceIndex(state.Sources, panelID)
	if index < 0 {
		status := m.operatorStatusLocked(panelID)
		m.operatorMu.Unlock()
		return status, nil
	}
	state.Sources = append(state.Sources[:index], state.Sources[index+1:]...)
	state.Revision++
	if err := m.persistOperatorsLocked(state); err != nil {
		m.operatorMu.Unlock()
		return OperatorRegistryStatus{}, apperr.Wrap("CONFIG_WRITE_FAILED", "operator registry could not be saved", err)
	}
	m.operators = state
	catalog := operatorCatalog(state)
	m.operatorMu.Unlock()
	m.applyOperatorCatalog(ctx, catalog)
	return m.OperatorStatus(panelID), nil
}

func (m *Manager) OperatorStatus(panelID string) OperatorRegistryStatus {
	panelID = strings.ToLower(strings.TrimSpace(panelID))
	m.operatorMu.RLock()
	status := m.operatorStatusLocked(panelID)
	m.operatorMu.RUnlock()
	return status
}

func (m *Manager) operatorStatusLocked(panelID string) OperatorRegistryStatus {
	status := OperatorRegistryStatus{
		PanelID: panelID, EffectiveRevision: m.operators.Revision,
		Active: len(m.operators.Sources) > 0, Targets: []OperatorSyncStatus{},
	}
	if index := operatorSourceIndex(m.operators.Sources, panelID); index >= 0 {
		status.SourcePresent = true
		status.SourceRevision = m.operators.Sources[index].Revision
	}
	m.mu.RLock()
	instances := make([]*instance, 0, len(m.instances))
	for _, current := range m.instances {
		instances = append(instances, current)
	}
	m.mu.RUnlock()
	for _, current := range instances {
		current.mu.RLock()
		if !model.IsProxyPlatform(current.cfg.Platform) && current.managed {
			target := current.operatorSync
			if target.State == "" {
				target = OperatorSyncStatus{
					InstanceID: current.cfg.InstanceID, Revision: m.operators.Revision,
					State: "pending", UpdatedAt: time.Now().UTC(),
				}
			}
			status.Targets = append(status.Targets, target)
		}
		current.mu.RUnlock()
	}
	sort.Slice(status.Targets, func(i, j int) bool {
		return status.Targets[i].InstanceID < status.Targets[j].InstanceID
	})
	return status
}

func (m *Manager) RestoreOperators(instanceID string) {
	current, err := m.lookup(instanceID)
	if err != nil {
		return
	}
	current.mu.RLock()
	platform := current.cfg.Platform
	current.mu.RUnlock()
	if model.IsProxyPlatform(platform) {
		return
	}
	m.operatorMu.RLock()
	catalog := operatorCatalog(m.operators)
	m.operatorMu.RUnlock()
	if catalog.Revision == 0 && !catalog.Active {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	m.applyOperatorToInstance(ctx, current, catalog)
}

func (m *Manager) applyOperatorCatalog(ctx context.Context, catalog OperatorCatalog) {
	m.mu.RLock()
	instances := make([]*instance, 0, len(m.instances))
	for _, current := range m.instances {
		instances = append(instances, current)
	}
	m.mu.RUnlock()
	var wait sync.WaitGroup
	for _, current := range instances {
		current.mu.RLock()
		managed := current.managed
		platform := current.cfg.Platform
		current.mu.RUnlock()
		if !managed || model.IsProxyPlatform(platform) {
			continue
		}
		wait.Add(1)
		go func(target *instance) {
			defer wait.Done()
			m.applyOperatorToInstance(ctx, target, catalog)
		}(current)
	}
	wait.Wait()
}

func (m *Manager) applyOperatorToInstance(
	ctx context.Context,
	current *instance,
	catalog OperatorCatalog,
) {
	current.mu.Lock()
	connection := current.pluginConnection
	connected := current.pluginConnected && connection != nil
	status := OperatorSyncStatus{
		InstanceID: current.cfg.InstanceID, Revision: catalog.Revision,
		State: "pending", UpdatedAt: time.Now().UTC(),
	}
	if connected {
		status.State = "syncing"
	}
	if current.operatorSync.Revision <= catalog.Revision {
		current.operatorSync = status
	}
	current.mu.Unlock()
	m.publishState(current)
	if !connected {
		return
	}
	var result OperatorApplyResult
	err := connection.Request(ctx, "operators.replace", catalog, &result)
	current.mu.Lock()
	if current.operatorSync.Revision <= catalog.Revision {
		status.State = "synced"
		status.Applied = result.Applied
		status.Removed = result.Removed
		if err != nil {
			status.State = "failed"
			status.Error = err.Error()
		}
		status.UpdatedAt = time.Now().UTC()
		current.operatorSync = status
	}
	current.mu.Unlock()
	m.publishState(current)
}

func (m *Manager) persistOperatorsLocked(state OperatorRegistryState) error {
	if m.saveOperators == nil {
		return nil
	}
	return m.saveOperators(cloneOperatorState(state))
}

func (c *PluginConnection) ReportOperatorDrift(report OperatorDriftReport) error {
	if len(report.Restored) > 10000 || len(report.Removed) > 10000 {
		return apperr.New("INVALID_REQUEST", "operator drift report is too large")
	}
	current := c.current
	current.mu.RLock()
	valid := current.sessionID == c.sessionID && current.pluginGeneration == c.generation && current.pluginConnected
	instanceID := current.cfg.InstanceID
	current.mu.RUnlock()
	if !valid {
		return apperr.New("UNAUTHENTICATED", "插件连接已失效")
	}
	c.manager.events.Publish(protocol.Event("operator.drift", map[string]any{
		"instance_id": instanceID, "revision": report.Revision,
		"restored_count": len(report.Restored), "removed_count": len(report.Removed),
	}))
	return nil
}

func normalizeOperatorState(state OperatorRegistryState) (OperatorRegistryState, error) {
	result := OperatorRegistryState{Revision: state.Revision, Sources: make([]OperatorSource, 0, len(state.Sources))}
	seen := make(map[string]struct{}, len(state.Sources))
	for _, source := range state.Sources {
		normalized, err := normalizeOperatorSource(source)
		if err != nil {
			return OperatorRegistryState{}, err
		}
		if _, exists := seen[normalized.PanelID]; exists {
			return OperatorRegistryState{}, errors.New("duplicate operator panel source")
		}
		seen[normalized.PanelID] = struct{}{}
		result.Sources = append(result.Sources, normalized)
	}
	sortOperatorSources(result.Sources)
	return result, nil
}

func normalizeOperatorSource(source OperatorSource) (OperatorSource, error) {
	source.PanelID = strings.ToLower(strings.TrimSpace(source.PanelID))
	if !validPanelID(source.PanelID) {
		return OperatorSource{}, apperr.New("INVALID_REQUEST", "panel_id is invalid")
	}
	if len(source.Operators) > 10000 {
		return OperatorSource{}, apperr.New("INVALID_REQUEST", "operator source is too large")
	}
	seen := make(map[string]struct{}, len(source.Operators))
	result := OperatorSource{PanelID: source.PanelID, Revision: source.Revision, Operators: make([]OperatorEntry, 0, len(source.Operators))}
	for _, item := range source.Operators {
		uuid, err := normalizeOperatorUUID(item.UUID)
		if err != nil {
			return OperatorSource{}, apperr.New("INVALID_REQUEST", "operator UUID is invalid")
		}
		if _, exists := seen[uuid]; exists {
			return OperatorSource{}, apperr.New("INVALID_REQUEST", "operator UUID is duplicated")
		}
		seen[uuid] = struct{}{}
		name := strings.TrimSpace(item.Name)
		if len([]rune(name)) > 64 {
			return OperatorSource{}, apperr.New("INVALID_REQUEST", "operator name is too long")
		}
		result.Operators = append(result.Operators, OperatorEntry{UUID: uuid, Name: name})
	}
	sort.Slice(result.Operators, func(i, j int) bool { return result.Operators[i].UUID < result.Operators[j].UUID })
	return result, nil
}

func operatorCatalog(state OperatorRegistryState) OperatorCatalog {
	merged := make(map[string]OperatorEntry)
	for _, source := range state.Sources {
		for _, item := range source.Operators {
			current, exists := merged[item.UUID]
			if !exists || current.Name == "" && item.Name != "" {
				merged[item.UUID] = item
			}
		}
	}
	operators := make([]OperatorEntry, 0, len(merged))
	for _, item := range merged {
		operators = append(operators, item)
	}
	sort.Slice(operators, func(i, j int) bool { return operators[i].UUID < operators[j].UUID })
	return OperatorCatalog{Revision: state.Revision, Active: len(state.Sources) > 0, Operators: operators}
}

func validPanelID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func normalizeOperatorUUID(value string) (string, error) {
	compact := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "")
	if len(compact) != 32 {
		return "", errors.New("invalid UUID length")
	}
	if _, err := hex.DecodeString(compact); err != nil {
		return "", err
	}
	return compact[:8] + "-" + compact[8:12] + "-" + compact[12:16] + "-" +
		compact[16:20] + "-" + compact[20:], nil
}

func operatorSourceIndex(sources []OperatorSource, panelID string) int {
	for index := range sources {
		if sources[index].PanelID == panelID {
			return index
		}
	}
	return -1
}

func sortOperatorSources(sources []OperatorSource) {
	sort.Slice(sources, func(i, j int) bool { return sources[i].PanelID < sources[j].PanelID })
}

func sameOperatorSource(left, right OperatorSource) bool {
	if left.PanelID != right.PanelID || left.Revision != right.Revision || len(left.Operators) != len(right.Operators) {
		return false
	}
	for index := range left.Operators {
		if left.Operators[index] != right.Operators[index] {
			return false
		}
	}
	return true
}

func cloneOperatorState(state OperatorRegistryState) OperatorRegistryState {
	result := OperatorRegistryState{Revision: state.Revision, Sources: make([]OperatorSource, len(state.Sources))}
	for index, source := range state.Sources {
		result.Sources[index] = source
		result.Sources[index].Operators = append([]OperatorEntry(nil), source.Operators...)
	}
	return result
}
