package server

import (
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
)

func TestEnsureServerWorkspaceCreatesStandaloneDirectory(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "servers", "example")
	if err := ensureServerWorkspace(model.ServerConfig{Type: "standalone", Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		t.Fatalf("standalone workspace was not created: %#v, %v", info, err)
	}
}

func TestEnsureServerWorkspaceCreatesOnlyMirrorImage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mirror")
	cfg := model.ServerConfig{
		Type: "mirror", RootPath: root, ImageDirectory: "image",
		ServerID: "example", InstanceCount: 2,
	}
	if err := ensureServerWorkspace(cfg); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "image")); err != nil || !info.IsDir() {
		t.Fatalf("mirror image was not created: %#v, %v", info, err)
	}
	for _, instanceID := range []string{"example_1", "example_2"} {
		if _, err := os.Stat(filepath.Join(root, instanceID)); !os.IsNotExist(err) {
			t.Fatalf("mirror instance directory %s should not be created: %v", instanceID, err)
		}
	}
}

func TestEnsureServerWorkspaceRejectsFileTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(target, []byte("not a directory"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := ensureServerWorkspace(model.ServerConfig{Type: "standalone", Workspace: target}); err == nil {
		t.Fatal("expected file workspace to be rejected")
	}
}

func TestCreateMissingWorkspaceLeavesItReadyForArchiveImport(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "new", "server")
	daemonConfig := config.Default()
	daemonConfig.Server.Port = 24444
	manager, err := supervisor.NewManager(daemonConfig, &eventbus.Bus{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store.NewServerStore(t.TempDir()), manager, nil)
	serverConfig := model.ServerConfig{
		SchemaVersion: model.SchemaVersion,
		Type:          "standalone",
		Platform:      "paper",
		ServerID:      "example",
		Name:          "Example",
		Workspace:     workspace,
		Port:          25565,
		Process: model.ProcessConfig{
			StartCommand: "java -jar server.jar",
			StopCommand:  "stop",
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	if _, err := service.Create(serverConfig); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("new workspace must remain empty before archive import: %#v", entries)
	}
}
