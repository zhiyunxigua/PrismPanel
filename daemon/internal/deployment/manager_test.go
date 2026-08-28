package deployment

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
)

func TestMirrorDeploymentTransaction(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "image")
	instancePath := filepath.Join(root, "bedwars_1")
	mustWrite(t, filepath.Join(image, "server.jar"), "new jar")
	mustWrite(t, filepath.Join(image, "config.txt"), "new config")
	mustWrite(t, filepath.Join(image, "world", "level.dat"), "image world")
	mustWrite(t, filepath.Join(image, ".prism-recycle-bin", "keep", "manifest.json"), "{}")
	mustWrite(t, filepath.Join(instancePath, "old.txt"), "old file")
	mustWrite(t, filepath.Join(instancePath, ".prism-recycle-bin", "keep", "manifest.json"), "{}")
	mustWrite(t, filepath.Join(instancePath, "world", "level.dat"), "saved world")
	mustWrite(t, filepath.Join(instancePath, "server.properties"), "motd=test\nserver-port=25500\n")

	serverConfig := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "mirror", ServerID: "bedwars", Name: "BedWars",
		RootPath: root, ImageDirectory: "image", InstanceCount: 1, Ports: []int{25571},
		Exclude: []model.ExcludeEntry{{Path: "world", Type: "directory"}},
		Process: model.ProcessConfig{
			StartCommand: "java -jar server.jar", StopCommand: "stop", StopTimeoutSeconds: 5,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	processManager, err := supervisor.NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{serverConfig})
	if err != nil {
		t.Fatal(err)
	}
	serverService := serverservice.NewService(
		store.NewServerStore(t.TempDir()), processManager, []model.ServerConfig{serverConfig},
	)
	manager := NewManager(serverService, processManager, 4)
	started, err := manager.Start("bedwars", []int{1})
	if err != nil {
		t.Fatal(err)
	}
	result := waitForDeployment(t, manager, started.TaskID)
	if result.Status != StatusCompleted {
		t.Fatalf("deployment failed: %#v", result)
	}
	if result.CopyConcurrency != 4 {
		t.Fatalf("unexpected copy concurrency: %d", result.CopyConcurrency)
	}
	if result.CopyStage != "finalizing" || result.CopyFilesDone != result.CopyFilesTotal ||
		result.CopyBytesDone != result.CopyBytesTotal {
		t.Fatalf("unexpected final copy progress: %#v", result)
	}
	assertContents(t, filepath.Join(instancePath, "config.txt"), "new config")
	assertContents(t, filepath.Join(instancePath, "world", "level.dat"), "saved world")
	if _, err := os.Stat(filepath.Join(instancePath, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old non-excluded file should be removed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(instancePath, ".prism-recycle-bin", "keep", "manifest.json")); err != nil {
		t.Fatalf("instance recycle bin was not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(image, ".prism-recycle-bin", "keep", "manifest.json")); err != nil {
		t.Fatalf("image recycle bin was not skipped: %v", err)
	}
	port, err := os.ReadFile(filepath.Join(instancePath, "server.properties"))
	if err != nil || string(port) != "server-port=25571\n" {
		t.Fatalf("unexpected server.properties: %q, %v", port, err)
	}
	residual, err := filepath.Glob(filepath.Join(root, ".*-bedwars_1-*"))
	if err != nil || len(residual) != 0 {
		t.Fatalf("deployment left residual directories: %v, %v", residual, err)
	}
}

func TestPluginConfigSyncUsesWhitelistAndPreservesTargetFiles(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "image")
	instancePath := filepath.Join(root, "bedwars_1")
	mustWrite(t, filepath.Join(image, "plugins", "Example", "config.yml"), "new config")
	mustWrite(t, filepath.Join(image, "plugins", "Example", "messages.JSON"), "new messages")
	mustWrite(t, filepath.Join(image, "plugins", "Example", "data.db"), "image database")
	mustWrite(t, filepath.Join(image, "plugins", "Example", "excluded.yml"), "image excluded")
	mustWrite(t, filepath.Join(image, "plugins", "top-level.yml"), "top level")
	mustWrite(t, filepath.Join(image, "plugins", "Example.jar"), "jar")
	mustWrite(t, filepath.Join(instancePath, "plugins", "Example", "config.yml"), "old config")
	mustWrite(t, filepath.Join(instancePath, "plugins", "Example", "data.db"), "instance database")
	mustWrite(t, filepath.Join(instancePath, "plugins", "Example", "excluded.yml"), "instance excluded")
	mustWrite(t, filepath.Join(instancePath, "plugins", "Example", "target-only.yml"), "target only")

	serverConfig := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "mirror", ServerID: "bedwars", Name: "BedWars",
		RootPath: root, ImageDirectory: "image", InstanceCount: 1, Ports: []int{25571},
		Exclude:                    []model.ExcludeEntry{{Path: filepath.Join("plugins", "Example", "excluded.yml"), Type: "file"}},
		PluginConfigSyncExtensions: []string{".yml", ".json"},
		Process: model.ProcessConfig{
			StartCommand: "java -jar server.jar", StopCommand: "stop", StopTimeoutSeconds: 5,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	processManager, err := supervisor.NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{serverConfig})
	if err != nil {
		t.Fatal(err)
	}
	serverService := serverservice.NewService(
		store.NewServerStore(t.TempDir()), processManager, []model.ServerConfig{serverConfig},
	)
	manager := NewManager(serverService, processManager, 2)
	started, err := manager.StartPluginConfigSync("bedwars", []int{1})
	if err != nil {
		t.Fatal(err)
	}
	result := waitForDeployment(t, manager, started.TaskID)
	if result.Status != StatusCompleted || result.Kind != TaskKindPluginConfigSync {
		t.Fatalf("config sync failed: %#v", result)
	}
	assertContents(t, filepath.Join(instancePath, "plugins", "Example", "config.yml"), "new config")
	assertContents(t, filepath.Join(instancePath, "plugins", "Example", "messages.JSON"), "new messages")
	assertContents(t, filepath.Join(instancePath, "plugins", "Example", "data.db"), "instance database")
	assertContents(t, filepath.Join(instancePath, "plugins", "Example", "excluded.yml"), "instance excluded")
	assertContents(t, filepath.Join(instancePath, "plugins", "Example", "target-only.yml"), "target only")
	for _, path := range []string{
		filepath.Join(instancePath, "plugins", "top-level.yml"),
		filepath.Join(instancePath, "plugins", "Example.jar"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unexpected top-level plugin file sync at %s: %v", path, err)
		}
	}
}

func TestImageSyncBackReplacesImageWithSelectedInstance(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "image")
	instancePath := filepath.Join(root, "bedwars_1")
	mustWrite(t, filepath.Join(image, "obsolete.txt"), "obsolete")
	mustWrite(t, filepath.Join(image, "config.txt"), "old image config")
	mustWrite(t, filepath.Join(instancePath, "config.txt"), "instance config")
	mustWrite(t, filepath.Join(instancePath, "world", "level.dat"), "updated world")
	mustWrite(t, filepath.Join(instancePath, "server.properties"), "server-port=25571\n")

	serverConfig := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "mirror", ServerID: "bedwars", Name: "BedWars",
		RootPath: root, ImageDirectory: "image", InstanceCount: 1, Ports: []int{25571},
		Exclude: []model.ExcludeEntry{{Path: "world", Type: "directory"}},
		Process: model.ProcessConfig{
			StartCommand: "java -jar server.jar", StopCommand: "stop", StopTimeoutSeconds: 5,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	processManager, err := supervisor.NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{serverConfig})
	if err != nil {
		t.Fatal(err)
	}
	serverService := serverservice.NewService(
		store.NewServerStore(t.TempDir()), processManager, []model.ServerConfig{serverConfig},
	)
	manager := NewManager(serverService, processManager, 2)
	started, err := manager.StartImageSyncBack("bedwars", []int{1})
	if err != nil {
		t.Fatal(err)
	}
	result := waitForDeployment(t, manager, started.TaskID)
	if result.Status != StatusCompleted || result.Kind != TaskKindImageSyncBack {
		t.Fatalf("image sync back failed: %#v", result)
	}
	assertContents(t, filepath.Join(image, "config.txt"), "instance config")
	assertContents(t, filepath.Join(image, "world", "level.dat"), "updated world")
	assertContents(t, filepath.Join(image, "server.properties"), "server-port=25571\n")
	if _, err := os.Stat(filepath.Join(image, "obsolete.txt")); !os.IsNotExist(err) {
		t.Fatalf("image-only file should be removed, got %v", err)
	}
	residual, err := filepath.Glob(filepath.Join(root, ".image-*-bedwars-*"))
	if err != nil || len(residual) != 0 {
		t.Fatalf("image sync left residual directories: %v, %v", residual, err)
	}
}

func TestImageSyncBackRequiresExactlyOneInstance(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "image", "server.jar"), "jar")
	serverConfig := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "mirror", ServerID: "bedwars", Name: "BedWars",
		RootPath: root, ImageDirectory: "image", InstanceCount: 2, Ports: []int{25571, 25572},
		Process: model.ProcessConfig{
			StartCommand: "java -jar server.jar", StopCommand: "stop", StopTimeoutSeconds: 5,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	processManager, err := supervisor.NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{serverConfig})
	if err != nil {
		t.Fatal(err)
	}
	serverService := serverservice.NewService(
		store.NewServerStore(t.TempDir()), processManager, []model.ServerConfig{serverConfig},
	)
	manager := NewManager(serverService, processManager, 1)
	for _, targets := range [][]int{nil, {1, 2}} {
		if _, err := manager.StartImageSyncBack("bedwars", targets); err == nil {
			t.Fatalf("expected target validation error for %v", targets)
		}
	}
}

func waitForDeployment(t *testing.T, manager *Manager, taskID string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Get(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if isFinished(snapshot.Status) {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("deployment did not finish")
	return Snapshot{}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o640); err != nil {
		t.Fatal(err)
	}
}

func assertContents(t *testing.T, path, expected string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != expected {
		t.Fatalf("unexpected contents for %s: %q", path, contents)
	}
}
