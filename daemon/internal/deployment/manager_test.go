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
	started, err := manager.StartPluginConfigSync("bedwars", []int{1}, nil)
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

// TestDetectConfigSyncDirsAdaptive 验证配置同步的自适应检测：
// 插件服检测 plugins/，mod 服检测 config/+plugins/，缺失关键目录时给出 issues。
func TestDetectConfigSyncDirsAdaptive(t *testing.T) {
	newManager := func(t *testing.T, platform string, dirs ...string) (*Manager, string) {
		t.Helper()
		root := t.TempDir()
		image := filepath.Join(root, "image")
		for _, dir := range dirs {
			mustWrite(t, filepath.Join(image, dir, "mod.toml"), "setting")
		}
		serverConfig := model.ServerConfig{
			SchemaVersion: model.SchemaVersion, Type: "mirror", Platform: platform,
			ServerID: "srv", Name: "Srv", RootPath: root, ImageDirectory: "image",
			InstanceCount: 1, Ports: []int{25571},
			Process: model.ProcessConfig{StartCommand: "java -jar server.jar", StopCommand: "stop", StopTimeoutSeconds: 5},
			Console: model.ConsoleConfig{Encoding: "utf-8"},
		}
		processManager, err := supervisor.NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{serverConfig})
		if err != nil {
			t.Fatal(err)
		}
		serverService := serverservice.NewService(
			store.NewServerStore(t.TempDir()), processManager, []model.ServerConfig{serverConfig},
		)
		return NewManager(serverService, processManager, 2), serverConfig.ServerID
	}

	// mod 服：config + plugins 都存在 → 推荐两者，无 issues
	manager, id := newManager(t, "fabric", "config", "plugins")
	result, err := manager.DetectConfigSyncDirs(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Recommended) != 2 || result.Recommended[0] != "config" {
		t.Fatalf("fabric with config+plugins: expected recommended [config plugins], got %v", result.Recommended)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("fabric with config+plugins: expected no issues, got %#v", result.Issues)
	}

	// mod 服：只有 plugins → 推荐 plugins，issue MOD_CONFIG_DIR_MISSING
	manager, id = newManager(t, "forge", "plugins")
	result, err = manager.DetectConfigSyncDirs(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Recommended) != 1 || result.Recommended[0] != "plugins" {
		t.Fatalf("forge without config: expected recommended [plugins], got %v", result.Recommended)
	}
	if !containsDetectIssue(result, "MOD_CONFIG_DIR_MISSING") {
		t.Fatalf("forge without config: expected MOD_CONFIG_DIR_MISSING issue, got %#v", result.Issues)
	}

	// 插件服：只有 plugins → 推荐 plugins，无 issues
	manager, id = newManager(t, "paper", "plugins")
	result, err = manager.DetectConfigSyncDirs(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Recommended) != 1 || result.Recommended[0] != "plugins" {
		t.Fatalf("paper with plugins: expected recommended [plugins], got %v", result.Recommended)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("paper with plugins: expected no issues, got %#v", result.Issues)
	}

	// 插件服：无任何目录 → 推荐空，issue NO_CONFIG_DIR_FOUND（无法确定）
	manager, id = newManager(t, "paper")
	result, err = manager.DetectConfigSyncDirs(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Recommended) != 0 {
		t.Fatalf("paper without dirs: expected empty recommended, got %v", result.Recommended)
	}
	if !containsDetectIssue(result, "NO_CONFIG_DIR_FOUND") {
		t.Fatalf("paper without dirs: expected NO_CONFIG_DIR_FOUND issue, got %#v", result.Issues)
	}

	// mod 服：无任何目录 → NO_CONFIG_DIR_FOUND（无法确定）
	manager, id = newManager(t, "fabric")
	result, err = manager.DetectConfigSyncDirs(id)
	if err != nil {
		t.Fatal(err)
	}
	if !containsDetectIssue(result, "NO_CONFIG_DIR_FOUND") {
		t.Fatalf("fabric without dirs: expected NO_CONFIG_DIR_FOUND issue, got %#v", result.Issues)
	}
}

func containsDetectIssue(result ConfigSyncDetectResult, code string) bool {
	for _, issue := range result.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

// TestValidateSyncDirectories 验证显式同步目录的安全校验：
// 拒绝绝对路径/.. 逃逸/不在允许集合的目录，防止同步范围逃出镜像源根。
func TestValidateSyncDirectories(t *testing.T) {
	mkCfg := func(platform string, configured ...string) model.ServerConfig {
		cfg := model.ServerConfig{SchemaVersion: model.SchemaVersion, Type: "mirror",
			Platform: platform, ServerID: "srv", Name: "Srv"}
		if len(configured) > 0 {
			cfg.ConfigSyncDirectories = append([]string(nil), configured...)
		}
		return cfg
	}

	// 合法：mod 服默认候选 config/plugins
	if err := validateSyncDirectories(mkCfg("fabric"), []string{"config", "plugins"}); err != nil {
		t.Fatalf("fabric config/plugins should pass: %v", err)
	}
	// 合法：插件服默认候选 plugins
	if err := validateSyncDirectories(mkCfg("paper"), []string{"plugins"}); err != nil {
		t.Fatalf("paper plugins should pass: %v", err)
	}
	// 合法：配置的目录集合（含嵌套）
	if err := validateSyncDirectories(mkCfg("fabric", "config", "mods/config"), []string{"mods/config"}); err != nil {
		t.Fatalf("configured nested dir should pass: %v", err)
	}

	// 拒绝：绝对路径
	if err := validateSyncDirectories(mkCfg("paper"), []string{"/etc"}); err == nil {
		t.Fatal("absolute path must be rejected")
	}
	// 拒绝：.. 逃逸
	for _, evil := range []string{"..", "../outside", "config/../../etc", "C:\\evil", `..\..\etc`} {
		if err := validateSyncDirectories(mkCfg("fabric"), []string{evil}); err == nil {
			t.Fatalf("path escape %q must be rejected", evil)
		}
	}
	// 拒绝：不在允许集合（mod 服但传 world）
	if err := validateSyncDirectories(mkCfg("fabric"), []string{"world"}); err == nil {
		t.Fatal("non-allowed dir must be rejected")
	}
	// 拒绝：配置了仅 plugins 时传 config（不在集合）
	if err := validateSyncDirectories(mkCfg("fabric", "plugins"), []string{"config"}); err == nil {
		t.Fatal("dir outside configured set must be rejected")
	}
}

func TestConfigSyncMultipleDirectories(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "image")
	instancePath := filepath.Join(root, "modbedwars_1")
	mustWrite(t, filepath.Join(image, "config", "fabric-api.json"), "new api config")
	mustWrite(t, filepath.Join(image, "config", "mymod.toml"), "new mod config")
	mustWrite(t, filepath.Join(image, "config", "submod", "extra.yml"), "new sub config")
	mustWrite(t, filepath.Join(image, "plugins", "Example", "config.yml"), "new plugin config")
	mustWrite(t, filepath.Join(image, "plugins", "top-level.yml"), "image top level")
	mustWrite(t, filepath.Join(image, "plugins", "Example", "data.db"), "image database")
	mustWrite(t, filepath.Join(instancePath, "config", "fabric-api.json"), "old api config")
	mustWrite(t, filepath.Join(instancePath, "config", "keep.txt"), "keep me")
	mustWrite(t, filepath.Join(instancePath, "config", "extra.json"), "target only")
	mustWrite(t, filepath.Join(instancePath, "plugins", "Example", "config.yml"), "old plugin config")
	mustWrite(t, filepath.Join(instancePath, "plugins", "top-level.yml"), "instance top level")

	serverConfig := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "mirror", Platform: "fabric", ServerID: "modbedwars", Name: "ModBedWars",
		RootPath: root, ImageDirectory: "image", InstanceCount: 1, Ports: []int{25571},
		ConfigSyncDirectories:      []string{"config", "plugins"},
		PluginConfigSyncExtensions: []string{".yml", ".json", ".toml"},
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
	started, err := manager.StartPluginConfigSync("modbedwars", []int{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := waitForDeployment(t, manager, started.TaskID)
	if result.Status != StatusCompleted || result.Kind != TaskKindPluginConfigSync {
		t.Fatalf("config sync failed: %#v", result)
	}
	if result.CopyStage != "finalizing" {
		t.Fatalf("unexpected final copy stage: %s", result.CopyStage)
	}
	// config 根目录：根级与子目录白名单文件都应覆盖。
	assertContents(t, filepath.Join(instancePath, "config", "fabric-api.json"), "new api config")
	assertContents(t, filepath.Join(instancePath, "config", "mymod.toml"), "new mod config")
	assertContents(t, filepath.Join(instancePath, "config", "submod", "extra.yml"), "new sub config")
	// config 根目录：非白名单与目标独有文件保持不变。
	assertContents(t, filepath.Join(instancePath, "config", "keep.txt"), "keep me")
	assertContents(t, filepath.Join(instancePath, "config", "extra.json"), "target only")
	// plugins 根目录：插件子目录内配置覆盖，根级文件与数据库文件不同步。
	assertContents(t, filepath.Join(instancePath, "plugins", "Example", "config.yml"), "new plugin config")
	assertContents(t, filepath.Join(instancePath, "plugins", "top-level.yml"), "instance top level")
	if _, err := os.Stat(filepath.Join(instancePath, "plugins", "Example", "data.db")); !os.IsNotExist(err) {
		t.Fatalf("unexpected database file sync: %v", err)
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
