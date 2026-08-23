package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
)

func TestProcessLifecycleAndConsole(t *testing.T) {
	helperPath := buildFakeJava(t)
	workspace := t.TempDir()
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "test", Name: "Test",
		Workspace: workspace, Port: 25565,
		Process: model.ProcessConfig{
			StartCommand: quotedPath(helperPath) + " --fake-server",
			StopCommand:  "stop", StopTimeoutSeconds: 5,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	cfg := startTestSessiond(t)
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

func TestManualDaemonRestartReattachesSession(t *testing.T) {
	helperPath := buildFakeJava(t)
	workspace := t.TempDir()
	dataDir := t.TempDir()
	server := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "test", Name: "Test",
		Workspace: workspace, Port: 25565,
		Process: model.ProcessConfig{
			StartCommand: quotedPath(helperPath) + " --fake-server",
			StopCommand:  "stop", StopTimeoutSeconds: 5,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	cfg := startTestSessiond(t)
	cfg.Process.SessionOrphanTimeoutSec = 30
	cfg.Storage.DataDir = dataDir
	first, err := NewManager(cfg, &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start("test"); err != nil {
		t.Fatal(err)
	}
	started, err := first.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	if started.State != StateRunning || started.PID == 0 {
		t.Fatalf("unexpected running snapshot: %#v", started)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if !pidAlive(started.PID) {
		t.Fatal("game process exited after daemon shutdown")
	}
	second, err := NewManager(cfg, &eventbus.Bus{}, []model.ServerConfig{server})
	if err != nil {
		t.Fatal(err)
	}
	second.RecoverSessions()
	recovered, err := second.Get("test")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != StateRunning || recovered.PID != started.PID {
		t.Fatalf("expected reattached session, got %#v", recovered)
	}
	if err := second.Kill("test"); err != nil {
		t.Fatal(err)
	}
}

func buildFakeJava(t *testing.T) string {
	t.Helper()
	helperName := "fakejava"
	if runtime.GOOS == "windows" {
		helperName += ".exe"
	}
	helperDir, err := filepath.Abs(filepath.Join("..", "..", ".tmp", "supervisor-tests"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(helperDir, helperName)
	if output, err := exec.Command("go", "build", "-o", helperPath, "./testdata/fakejava").CombinedOutput(); err != nil {
		t.Fatalf("build fake java: %v\n%s", err, output)
	}
	return helperPath
}

func startTestSessiond(t *testing.T) config.Config {
	t.Helper()
	helperDir, err := filepath.Abs(filepath.Join("..", "..", ".tmp", "supervisor-tests"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "prism-sessiond"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(helperDir, name)
	sessiondDir, err := filepath.Abs(filepath.Join("..", "..", "..", "sessiond"))
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/prism-sessiond")
	build.Dir = sessiondDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build prism-sessiond: %v\n%s", err, output)
	}
	root := t.TempDir()
	listen := filepath.Join(root, "session.sock")
	tokenFile := filepath.Join(root, "token")
	if err := os.WriteFile(tokenFile, []byte("test-session-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "sessiond.yaml")
	contents := "listen: " + strconv.Quote(listen) + "\nstate_dir: " + strconv.Quote(filepath.Join(root, "state")) + "\ntoken_file: " + strconv.Quote(tokenFile) + "\norphan_timeout_seconds: 30\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--config", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start prism-sessiond: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(listen); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prism-sessiond did not create listen endpoint")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cfg := config.Default()
	cfg.Process.SessionSocket = listen
	cfg.Process.SessionTokenFile = tokenFile
	cfg.Storage.DataDir = t.TempDir()
	return cfg
}

func quotedPath(path string) string {
	return string('"') + path + string('"')
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
			t.Fatalf("timed out waiting for console line %q after start", expected)
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
