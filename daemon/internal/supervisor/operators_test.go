package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"
	"time"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
)

const (
	operatorPanelA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	operatorPanelB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	operatorUUIDA  = "123e4567-e89b-12d3-a456-426614174000"
	operatorUUIDB  = "223e4567-e89b-12d3-a456-426614174000"
)

func TestOperatorCatalogMergesPanelSources(t *testing.T) {
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var saved OperatorRegistryState
	if err := manager.ConfigureOperators(OperatorRegistryState{}, func(state OperatorRegistryState) error {
		saved = cloneOperatorState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReplaceOperatorSource(context.Background(), OperatorSource{
		PanelID: operatorPanelA, Revision: 1,
		Operators: []OperatorEntry{{UUID: operatorUUIDA, Name: "Steve"}, {UUID: operatorUUIDB, Name: "Alex"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReplaceOperatorSource(context.Background(), OperatorSource{
		PanelID: operatorPanelB, Revision: 1,
		Operators: []OperatorEntry{{UUID: operatorUUIDB, Name: "Alex"}},
	}); err != nil {
		t.Fatal(err)
	}
	manager.operatorMu.RLock()
	catalog := operatorCatalog(manager.operators)
	manager.operatorMu.RUnlock()
	if !catalog.Active || len(catalog.Operators) != 2 {
		t.Fatalf("unexpected merged catalog: %+v", catalog)
	}
	if len(saved.Sources) != 2 {
		t.Fatalf("expected both sources to be persisted: %+v", saved)
	}

	if _, err := manager.RemoveOperatorSource(context.Background(), operatorPanelA); err != nil {
		t.Fatal(err)
	}
	manager.operatorMu.RLock()
	catalog = operatorCatalog(manager.operators)
	manager.operatorMu.RUnlock()
	if len(catalog.Operators) != 1 || catalog.Operators[0].UUID != operatorUUIDB {
		t.Fatalf("unexpected catalog after removing panel A: %+v", catalog)
	}
}

func TestOperatorSourceRevisionIsMonotonic(t *testing.T) {
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureOperators(OperatorRegistryState{}, nil); err != nil {
		t.Fatal(err)
	}
	source := OperatorSource{
		PanelID: operatorPanelA, Revision: 2,
		Operators: []OperatorEntry{{UUID: operatorUUIDA, Name: "Steve"}},
	}
	if _, err := manager.ReplaceOperatorSource(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	source.Revision = 1
	if _, err := manager.ReplaceOperatorSource(context.Background(), source); err == nil {
		t.Fatal("expected stale operator source revision to be rejected")
	}
}

// fabricInstanceWithPlugin 构造一个已注册插件连接的 fabric 实例，
// 供 OP 同步 capability 驱动测试复用。
func fabricInstanceWithPlugin(
	t *testing.T,
	manager *Manager,
	capabilities []string,
) (*PluginConnection, *instance) {
	t.Helper()
	current, err := manager.lookup("fab")
	if err != nil {
		t.Fatal(err)
	}
	token := "temporary-token"
	current.mu.Lock()
	current.state = StateRunning
	current.pid = os.Getpid()
	current.sessionID = "session"
	current.pluginTokenHash = sha256.Sum256([]byte(token))
	current.pluginTokenSet = true
	current.mu.Unlock()
	connection, err := manager.RegisterPlugin(
		"fab", "session", token, os.Getpid(), "fabric", capabilities,
	)
	if err != nil {
		t.Fatal(err)
	}
	return connection, current
}

// TestFabricInstanceRestoresOperatorsWhenCapabilityDeclared 验证移除 mod 平台硬跳过后：
// fabric 实例声明 operators.sync 时，daemon 恢复（RestoreOperators）会自动补发
// operators.replace，实例出现在 OP targets 且同步成功（capability 驱动）。
func TestFabricInstanceRestoresOperatorsWhenCapabilityDeclared(t *testing.T) {
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "fab", Name: "Fabric",
		Platform:  "fabric",
		Workspace: t.TempDir(), Port: 25566,
		Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureOperators(OperatorRegistryState{}, nil); err != nil {
		t.Fatal(err)
	}
	// 插件尚未连接时 catalog 应用只记 pending，不发命令。
	if _, err := manager.ReplaceOperatorSource(context.Background(), OperatorSource{
		PanelID: operatorPanelA, Revision: 1,
		Operators: []OperatorEntry{{UUID: operatorUUIDA, Name: "Steve"}},
	}); err != nil {
		t.Fatal(err)
	}
	connection, current := fabricInstanceWithPlugin(t, manager, []string{"mod.inventory", "operators.sync"})
	defer connection.Close()
	go respondToOperatorReplace(t, connection)

	manager.RestoreOperators("fab")

	waitForSyncState(t, current, "synced")
	current.mu.RLock()
	sync := current.operatorSync
	current.mu.RUnlock()
	if sync.Applied != 1 || sync.Removed != 0 || sync.Error != "" {
		t.Fatalf("unexpected fabric operator sync result: %+v", sync)
	}
	status := manager.OperatorStatus(operatorPanelA)
	if len(status.Targets) != 1 || status.Targets[0].InstanceID != "fab" || status.Targets[0].State != "synced" {
		t.Fatalf("expected fabric instance in operator targets: %+v", status.Targets)
	}
}

// TestFabricInstanceWithoutOperatorCapabilityFailsNaturally 验证 capability 驱动的自然拒绝：
// 插件未声明 operators.sync 时，恢复补发的 operators.replace 由 connection.Request
// 的 capability 检查拒绝，实例状态为 failed 而非跳过或误报成功。
func TestFabricInstanceWithoutOperatorCapabilityFailsNaturally(t *testing.T) {
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "fab", Name: "Fabric",
		Platform:  "fabric",
		Workspace: t.TempDir(), Port: 25566,
		Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureOperators(OperatorRegistryState{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReplaceOperatorSource(context.Background(), OperatorSource{
		PanelID: operatorPanelA, Revision: 1,
		Operators: []OperatorEntry{{UUID: operatorUUIDA, Name: "Steve"}},
	}); err != nil {
		t.Fatal(err)
	}
	connection, current := fabricInstanceWithPlugin(t, manager, []string{"mod.inventory"})
	defer connection.Close()

	manager.RestoreOperators("fab")

	waitForSyncState(t, current, "failed")
	current.mu.RLock()
	sync := current.operatorSync
	current.mu.RUnlock()
	if sync.Error == "" {
		t.Fatalf("expected capability rejection error, got: %+v", sync)
	}
	status := manager.OperatorStatus(operatorPanelA)
	if len(status.Targets) != 1 || status.Targets[0].InstanceID != "fab" || status.Targets[0].State != "failed" {
		t.Fatalf("expected fabric instance failed in operator targets: %+v", status.Targets)
	}
}

func respondToOperatorReplace(t *testing.T, connection *PluginConnection) {
	t.Helper()
	for {
		select {
		case request := <-connection.Outgoing():
			data, _ := json.Marshal(OperatorApplyResult{Revision: 1, Applied: 1, Removed: 0})
			connection.HandleResponse(PluginResponse{RequestID: request.RequestID, Success: true, Data: data})
		case <-connection.Done():
			return
		}
	}
}

func waitForSyncState(t *testing.T, current *instance, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current.mu.RLock()
		got := current.operatorSync.State
		current.mu.RUnlock()
		if got == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	current.mu.RLock()
	defer current.mu.RUnlock()
	t.Fatalf("operator sync never reached %q: %+v", state, current.operatorSync)
}
