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
