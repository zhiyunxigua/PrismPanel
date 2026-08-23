package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
)

func TestUploadInstanceDetectsPluginNameAndReplacesExistingFile(t *testing.T) {
	service, _, workspaces := newUploadTestService(t, 1)
	pluginDir := filepath.Join(workspaces[0], "plugins")
	if err := os.MkdirAll(pluginDir, 0o750); err != nil {
		t.Fatal(err)
	}
	existingPath := filepath.Join(pluginDir, "custom-name.jar")
	if err := os.WriteFile(existingPath, pluginJAR(t, "Example", "1.0", "com.example.Main"), 0o640); err != nil {
		t.Fatal(err)
	}
	incoming := filepath.Join(t.TempDir(), "renamed-upload.jar")
	if err := os.WriteFile(incoming, pluginJAR(t, "Example", "2.0", "com.example.Main"), 0o640); err != nil {
		t.Fatal(err)
	}

	conflict, err := service.UploadInstance("group_1", incoming, "renamed-upload.jar", false)
	var apiError *apperr.Error
	if !errors.As(err, &apiError) || apiError.Code != "PLUGIN_EXISTS" {
		t.Fatalf("expected plugin conflict, got %#v, %v", conflict, err)
	}
	if conflict.ExistingFile != "custom-name.jar" || conflict.ExistingVersion != "1.0" {
		t.Fatalf("unexpected conflict details: %#v", conflict)
	}

	replaced, err := service.UploadInstance("group_1", incoming, "renamed-upload.jar", true)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Outcome != "replaced" || replaced.SourceFile != "custom-name.jar" || !replaced.Replaced {
		t.Fatalf("unexpected replacement result: %#v", replaced)
	}
	items, warnings := service.cache.scan(workspaces[0])
	if len(warnings) != 0 || len(items) != 1 || items[0].Version != "2.0" {
		t.Fatalf("unexpected installed plugin: %#v, %#v", items, warnings)
	}
}

func TestUploadInstanceOnlyChangesTargetInstance(t *testing.T) {
	service, _, workspaces := newUploadTestService(t, 2)
	for _, workspace := range workspaces {
		pluginDir := filepath.Join(workspace, "plugins")
		if err := os.MkdirAll(pluginDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pluginDir, "Example.jar"), pluginJAR(t, "Example", "1.0", "com.example.Main"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	incoming := filepath.Join(t.TempDir(), "Example-2.0.jar")
	if err := os.WriteFile(incoming, pluginJAR(t, "Example", "2.0", "com.example.Main"), 0o640); err != nil {
		t.Fatal(err)
	}

	result, err := service.UploadInstance("group_1", incoming, "Example-2.0.jar", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "replaced" {
		t.Fatalf("unexpected upload result: %#v", result)
	}
	first, firstWarnings := service.cache.scan(workspaces[0])
	second, secondWarnings := service.cache.scan(workspaces[1])
	if len(firstWarnings) != 0 || len(first) != 1 || first[0].Version != "2.0" {
		t.Fatalf("target instance was not replaced: %#v, %#v", first, firstWarnings)
	}
	if len(secondWarnings) != 0 || len(second) != 1 || second[0].Version != "1.0" {
		t.Fatalf("non-target instance changed: %#v, %#v", second, secondWarnings)
	}
}

func newUploadTestService(t *testing.T, count int) (*Service, *supervisor.Manager, []string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	configs := make([]model.ServerConfig, 0, count)
	workspaces := make([]string, 0, count)
	for index := 1; index <= count; index++ {
		serverID := fmt.Sprintf("group_%d", index)
		workspace := filepath.Join(root, serverID)
		if err := os.MkdirAll(workspace, 0o750); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, model.ServerConfig{
			SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: serverID, Name: serverID,
			Workspace: workspace, Port: 25564 + index,
			Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
			Console: model.ConsoleConfig{Encoding: "utf-8"},
		})
		workspaces = append(workspaces, workspace)
	}
	manager, err := supervisor.NewManager(cfg, &eventbus.Bus{}, configs)
	if err != nil {
		t.Fatal(err)
	}
	servers := serverservice.NewService(store.NewServerStore(filepath.Join(root, "servers")), manager, configs)
	service, err := NewService(manager, servers, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	return service, manager, workspaces
}
