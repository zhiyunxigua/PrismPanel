package supervisor

import (
	"testing"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
)

func TestWithStoppedFileMutationRequiresInactiveInstance(t *testing.T) {
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
	current.mu.Lock()
	current.state = StateRunning
	current.mu.Unlock()

	called := false
	if err := manager.WithStoppedFileMutation("test", func(string) error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("expected running instance mutation to be rejected")
	}
	if called {
		t.Fatal("mutation ran while instance was active")
	}

	current.mu.Lock()
	current.state = StateStopped
	current.mu.Unlock()
	if err := manager.WithStoppedFileMutation("test", func(string) error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("mutation did not run for stopped instance")
	}
}
