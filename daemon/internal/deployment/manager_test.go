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
	mustWrite(t, filepath.Join(instancePath, "old.txt"), "old file")
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
	port, err := os.ReadFile(filepath.Join(instancePath, "server.properties"))
	if err != nil || string(port) != "server-port=25571\n" {
		t.Fatalf("unexpected server.properties: %q, %v", port, err)
	}
	residual, err := filepath.Glob(filepath.Join(root, ".*-bedwars_1-*"))
	if err != nil || len(residual) != 0 {
		t.Fatalf("deployment left residual directories: %v, %v", residual, err)
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
