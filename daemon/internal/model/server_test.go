package model

import (
	"path/filepath"
	"testing"
)

func TestMirrorInstances(t *testing.T) {
	cfg := ServerConfig{
		SchemaVersion: SchemaVersion, Type: "mirror", Platform: "paper", ServerID: "bedwars", Name: "BedWars",
		RootPath: filepath.Join(t.TempDir(), "bedwars"), ImageDirectory: "image", InstanceCount: 2,
		Ports:   []int{25571, 25572},
		Process: ProcessConfig{StartCommand: "java -jar server.jar nogui", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: ConsoleConfig{Encoding: "utf-8"},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	instances := cfg.Instances()
	if len(instances) != 2 || instances[0].InstanceID != "bedwars_1" || instances[1].Port != 25572 {
		t.Fatalf("unexpected derived instances: %#v", instances)
	}
}

func TestMissingStartCommandIsRejected(t *testing.T) {
	cfg := ServerConfig{
		SchemaVersion: SchemaVersion, Type: "standalone", ServerID: "legacy", Name: "Legacy",
		Workspace: t.TempDir(), Port: 25565,
		Process: ProcessConfig{StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: ConsoleConfig{Encoding: "utf-8"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected configuration without start_command to be rejected")
	}
}

func TestConsoleEncodingNormalization(t *testing.T) {
	for input, expected := range map[string]string{
		"": "utf-8", "UTF8": "utf-8", "UTF-8": "utf-8", "GBK": "gbk",
	} {
		cfg := ServerConfig{Console: ConsoleConfig{Encoding: input}}
		cfg.Normalize()
		if cfg.Console.Encoding != expected {
			t.Fatalf("normalize %q: expected %q, got %q", input, expected, cfg.Console.Encoding)
		}
	}
}

func TestUnsupportedConsoleEncodingIsRejected(t *testing.T) {
	cfg := ServerConfig{
		SchemaVersion: SchemaVersion, Type: "standalone", ServerID: "invalid-encoding", Name: "Invalid",
		Workspace: t.TempDir(), Port: 25565,
		Process: ProcessConfig{StartCommand: "java -jar server.jar", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: ConsoleConfig{Encoding: "big5"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported console encoding to be rejected")
	}
}

func TestPluginConfigSyncExtensionsNormalization(t *testing.T) {
	cfg := ServerConfig{Type: "mirror", PluginConfigSyncExtensions: []string{"YML", ".Json", ".yml", "  .toml  "}}
	cfg.Normalize()
	expected := []string{".yml", ".json", ".toml"}
	if len(cfg.PluginConfigSyncExtensions) != len(expected) {
		t.Fatalf("unexpected normalized extensions: %#v", cfg.PluginConfigSyncExtensions)
	}
	for index := range expected {
		if cfg.PluginConfigSyncExtensions[index] != expected[index] {
			t.Fatalf("unexpected normalized extensions: %#v", cfg.PluginConfigSyncExtensions)
		}
	}
	if !cfg.AllowsPluginConfigSync("config.YML") || cfg.AllowsPluginConfigSync("data.db") {
		t.Fatal("plugin config suffix matching did not use the normalized whitelist")
	}
}

func TestPluginConfigSyncExtensionsDefault(t *testing.T) {
	cfg := ServerConfig{Type: "mirror"}
	cfg.Normalize()
	if len(cfg.PluginConfigSyncExtensions) == 0 || !cfg.AllowsPluginConfigSync("config.yml") {
		t.Fatalf("expected a conservative default whitelist, got %#v", cfg.PluginConfigSyncExtensions)
	}
}

func TestPluginConfigSyncExtensionsDefaultPassValidation(t *testing.T) {
	cfg := ServerConfig{
		SchemaVersion: SchemaVersion, Type: "mirror", Platform: "paper", ServerID: "default-suffixes",
		Name: "Default suffixes", RootPath: t.TempDir(), ImageDirectory: "image", InstanceCount: 1,
		Ports: []int{25571}, Process: ProcessConfig{StartCommand: "java -jar server.jar", StopCommand: "stop"},
		Console: ConsoleConfig{Encoding: "utf-8"},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default plugin config suffixes should be valid: %v", err)
	}
}

func TestConfigSyncDirectoriesDefaultByPlatform(t *testing.T) {
	paper := ServerConfig{Type: "mirror", Platform: "paper"}
	paper.Normalize()
	if len(paper.ConfigSyncDirectories) != 1 || paper.ConfigSyncDirectories[0] != "plugins" {
		t.Fatalf("paper mirror should default to [plugins], got %#v", paper.ConfigSyncDirectories)
	}
	fabric := ServerConfig{Type: "mirror", Platform: "fabric"}
	fabric.Normalize()
	if len(fabric.ConfigSyncDirectories) != 2 ||
		fabric.ConfigSyncDirectories[0] != "config" || fabric.ConfigSyncDirectories[1] != "plugins" {
		t.Fatalf("fabric mirror should default to [config plugins], got %#v", fabric.ConfigSyncDirectories)
	}
	forge := ServerConfig{Type: "mirror", Platform: "forge"}
	forge.Normalize()
	if len(forge.ConfigSyncDirectories) != 2 ||
		forge.ConfigSyncDirectories[0] != "config" || forge.ConfigSyncDirectories[1] != "plugins" {
		t.Fatalf("forge mirror should default to [config plugins], got %#v", forge.ConfigSyncDirectories)
	}
	standalone := ServerConfig{Type: "standalone", Platform: "fabric"}
	standalone.Normalize()
	if len(standalone.ConfigSyncDirectories) != 0 {
		t.Fatalf("standalone should not keep config sync directories, got %#v", standalone.ConfigSyncDirectories)
	}
}

func TestConfigSyncDirectoriesNormalization(t *testing.T) {
	// 大小写保留：Linux 上目录名区分大小写，显式传入的 "CONFIG" 应原样保留，
	// 不做小写化（否则真实名为 Config 的目录会同步失败且无提示），只 trim + 去重 + 空值处理。
	cfg := ServerConfig{Type: "mirror", Platform: "fabric", ConfigSyncDirectories: []string{"CONFIG", " config ", "plugins", "config", ""}}
	cfg.Normalize()
	expected := []string{"CONFIG", "config", "plugins"}
	if len(cfg.ConfigSyncDirectories) != len(expected) {
		t.Fatalf("unexpected normalized directories: %#v", cfg.ConfigSyncDirectories)
	}
	for index := range expected {
		if cfg.ConfigSyncDirectories[index] != expected[index] {
			t.Fatalf("unexpected normalized directories: %#v", cfg.ConfigSyncDirectories)
		}
	}
}

func TestConfigSyncDirectoriesValidation(t *testing.T) {
	for _, invalid := range []string{"../escape", "/absolute", "..", "."} {
		cfg := ServerConfig{
			SchemaVersion: SchemaVersion, Type: "mirror", Platform: "fabric", ServerID: "invalid-dir",
			Name: "Invalid dir", RootPath: t.TempDir(), ImageDirectory: "image", InstanceCount: 1,
			Ports: []int{25571}, ConfigSyncDirectories: []string{invalid},
			Process: ProcessConfig{StartCommand: "java -jar server.jar", StopCommand: "stop"},
			Console: ConsoleConfig{Encoding: "utf-8"},
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("config_sync_directories %q should be rejected", invalid)
		}
	}
}

func TestModPlatformSupport(t *testing.T) {
	for _, platform := range []string{"paper", "spigot", "fabric", "forge", "velocity", "bungee"} {
		if !IsSupportedPlatform(platform) {
			t.Fatalf("platform %q should be supported", platform)
		}
	}
	if IsSupportedPlatform("vanilla") {
		t.Fatal("vanilla should not be supported")
	}
	for _, platform := range []string{"fabric", "forge"} {
		if !IsModPlatform(platform) || ModTypeForPlatform(platform) != platform {
			t.Fatalf("platform %q should be a mod platform", platform)
		}
	}
	if IsModPlatform("paper") || ModTypeForPlatform("paper") != "" {
		t.Fatal("paper should not be a mod platform")
	}
	if PluginTypeForPlatform("fabric") != "fabric" || PluginTypeForPlatform("forge") != "forge" {
		t.Fatal("mod platforms should map to their own plugin type")
	}
	if PluginTypeForPlatform("paper") != "spigot" || PluginTypeForPlatform("velocity") != "velocity" {
		t.Fatal("plugin type mapping for paper/velocity is wrong")
	}
}

func TestFabricMirrorPassesValidation(t *testing.T) {
	cfg := ServerConfig{
		SchemaVersion: SchemaVersion, Type: "mirror", Platform: "fabric", ServerID: "modbedwars",
		Name: "ModBedWars", RootPath: t.TempDir(), ImageDirectory: "image", InstanceCount: 1,
		Ports: []int{25571}, Process: ProcessConfig{StartCommand: "java -jar server.jar", StopCommand: "stop"},
		Console: ConsoleConfig{Encoding: "utf-8"},
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("fabric mirror should be valid: %v", err)
	}
	if PluginTypeForPlatform(cfg.Platform) != "fabric" {
		t.Fatalf("fabric mirror plugin type mismatch: %s", PluginTypeForPlatform(cfg.Platform))
	}
}
