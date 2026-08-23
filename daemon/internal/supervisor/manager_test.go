package supervisor

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
)

func TestProcessLifecycleAndConsole(t *testing.T) {
	helperName := "fakejava"
	if runtime.GOOS == "windows" {
		helperName += ".exe"
	}
	helperPath := filepath.Join(t.TempDir(), helperName)
	build := exec.Command("go", "build", "-o", helperPath, "./testdata/fakejava")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake java: %v\n%s", err, output)
	}
	workspace := t.TempDir()
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "test", Name: "Test",
		Workspace: workspace, Port: 25565,
		Process: model.ProcessConfig{
			StartCommand: string('"') + helperPath + string('"') + " --fake-server",
			StopCommand:  "stop", StopTimeoutSeconds: 5,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	cfg := config.Default()
	manager, err := NewManager(cfg, &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	history, lines, cancel, err := manager.Subscribe("test", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if len(history) != 0 {
		t.Fatalf("unexpected console history: %#v", history)
	}
	if err := manager.Start("test"); err != nil {
		t.Fatal(err)
	}
	defer manager.Kill("test")
	waitForConsole(t, lines, "fake server ready")
	if err := manager.Command("test", "say hello"); err != nil {
		t.Fatal(err)
	}
	waitForConsole(t, lines, "command: say hello")
	if err := manager.Stop("test"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateStopped || snapshot.PID != 0 || snapshot.RuntimePort != nil {
		t.Fatalf("unexpected stopped snapshot: %#v", snapshot)
	}
	if err := manager.Start("test"); err != nil {
		t.Fatal(err)
	}
	waitForConsoleType(t, lines, "console.reset")
	secondHistory, _, secondCancel, err := manager.Subscribe("test", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer secondCancel()
	for _, line := range secondHistory {
		if strings.Contains(line.Content, "command: say hello") {
			t.Fatalf("old session output leaked into new history: %#v", secondHistory)
		}
	}
}

func TestManagerRegistersMirrorInstances(t *testing.T) {
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "mirror", ServerID: "bedwars", Name: "BedWars",
		RootPath: t.TempDir(), ImageDirectory: "image", InstanceCount: 2,
		Ports: []int{25571, 25572},
		Process: model.ProcessConfig{
			StartCommand: "java -jar server.jar", StopCommand: "stop", StopTimeoutSeconds: 30,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	instances := manager.List()
	if len(instances) != 2 {
		t.Fatalf("expected two mirror instances, got %d", len(instances))
	}
	if instances[0].InstanceID != "bedwars_1" || instances[0].ConfiguredPort != 25571 {
		t.Fatalf("unexpected first mirror instance: %#v", instances[0])
	}
	if instances[1].InstanceID != "bedwars_2" || instances[1].ConfiguredPort != 25572 {
		t.Fatalf("unexpected second mirror instance: %#v", instances[1])
	}
}

func waitForConsole(t *testing.T, lines <-chan ConsoleLine, expected string) {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case line := <-lines:
			if strings.Contains(line.Content, expected) {
				return
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for console line %q", expected)
		}
	}
}

func waitForConsoleType(t *testing.T, lines <-chan ConsoleLine, expected string) {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case line := <-lines:
			if line.Type == expected {
				return
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for console event %q", expected)
		}
	}
}
