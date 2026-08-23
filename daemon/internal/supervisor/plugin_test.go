package supervisor

import (
	"crypto/sha256"
	"os"
	"testing"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
)

func TestPluginConnectionIsBoundToRunningInstance(t *testing.T) {
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "test", Name: "Test",
		Workspace: t.TempDir(), Port: 25565,
		Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.lookup("test")
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

	// 默认配置 Normalize 后平台为 paper，RegisterPlugin 的平台必须与
	// PluginTypeForPlatform("paper") = "paper" 一致。
	if _, err := manager.RegisterPlugin("test", "session", "wrong", os.Getpid(), "paper", nil); err == nil {
		t.Fatal("expected invalid plugin token to be rejected")
	}
	connection, err := manager.RegisterPlugin("test", "session", token, os.Getpid(), "paper", []string{"telemetry"})
	if err != nil {
		t.Fatal(err)
	}
	tps, mspt := 20.0, 10.0
	report := PluginReport{
		TPS: &tps, MSPT: &mspt, OnlinePlayers: intPointer(1), MaxPlayers: intPointer(20),
		Players: []PlayerSnapshot{{UUID: "player", Name: "Steve", Ping: 30}},
		Plugins: []LoadedPlugin{{ID: "prism", Name: "PrismMC", Version: "0.1.0", Enabled: true}},
	}
	if err := connection.Update(report); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.PluginConnected || snapshot.TPS == nil || *snapshot.TPS != 20 ||
		len(snapshot.Players) != 1 || len(snapshot.Plugins) != 1 {
		t.Fatalf("unexpected plugin snapshot: %#v", snapshot)
	}
	connection.Close()
	snapshot, _ = manager.Get("test")
	if snapshot.PluginConnected {
		t.Fatal("expected plugin to be disconnected")
	}
}

// TestModReportWithoutPlayersStaysNil 验证 mod 上报（无玩家数据）时快照在线人数保持 nil，
// 避免 Fabric/Forge 这类无法采集玩家数的平台显示假的 0/0。
func TestModReportWithoutPlayersStaysNil(t *testing.T) {
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "modtest", Name: "Mod Test",
		Platform:  "fabric",
		Workspace: t.TempDir(), Port: 25565,
		Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.lookup("modtest")
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
	connection, err := manager.RegisterPlugin("modtest", "session", token, os.Getpid(), "fabric", []string{"mod.inventory"})
	if err != nil {
		t.Fatal(err)
	}
	report := PluginReport{
		Plugins: []LoadedPlugin{{ID: "sodium", Name: "Sodium", Version: "0.5.11+mc1.21.1",
			SourceFile: "sodium-fabric-0.5.11+mc1.21.1.jar", Enabled: true}},
	}
	if err := connection.Update(report); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Get("modtest")
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.PluginConnected || snapshot.OnlinePlayers != nil || snapshot.MaxPlayers != nil {
		t.Fatalf("mod report without players must keep online players nil: %#v", snapshot)
	}
	if len(snapshot.Plugins) != 1 || snapshot.Plugins[0].ID != "sodium" {
		t.Fatalf("mod runtime plugins not propagated: %#v", snapshot.Plugins)
	}
}

func TestPluginReportValidationRejectsInvalidPlayers(t *testing.T) {
	tps := 20.0
	cases := []PluginReport{
		{OnlinePlayers: intPointer(-1)},
		{MaxPlayers: intPointer(-1)},
		{OnlinePlayers: intPointer(10), MaxPlayers: intPointer(5)},
		{TPS: &tps, MSPT: &tps, JVMThreads: -1},
		{Plugins: make([]LoadedPlugin, 5001)},
	}
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "valid", Name: "Valid",
		Workspace: t.TempDir(), Port: 25565,
		Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.lookup("valid")
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
	connection, err := manager.RegisterPlugin("valid", "session", token, os.Getpid(), "paper", nil)
	if err != nil {
		t.Fatal(err)
	}
	for index, report := range cases {
		if err := connection.Update(report); err == nil {
			t.Fatalf("case %d: expected invalid plugin report to be rejected", index)
		}
	}
	// 仅玩家指针省略的合法上报（mod 场景）必须通过。
	if err := connection.Update(PluginReport{Plugins: []LoadedPlugin{{Name: "M", Enabled: true}}}); err != nil {
		t.Fatalf("valid mod-style report rejected: %v", err)
	}
}
