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
		SchemaVersion: 1, Type: "standalone", ServerID: "test", Name: "Test",
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

	if _, err := manager.RegisterPlugin("test", "session", "wrong", os.Getpid()); err == nil {
		t.Fatal("expected invalid plugin token to be rejected")
	}
	connection, err := manager.RegisterPlugin("test", "session", token, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	tps, mspt := 20.0, 10.0
	report := PluginReport{
		TPS: &tps, MSPT: &mspt, OnlinePlayers: 1, MaxPlayers: 20,
		Players: []PlayerSnapshot{{UUID: "player", Name: "Steve", Ping: 30}},
		Plugins: []LoadedPlugin{{Name: "PrismMC", Version: "0.1.0", Enabled: true}},
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
