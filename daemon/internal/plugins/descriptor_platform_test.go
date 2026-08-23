package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFabricModDeepMetadataParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PrismMod-2.1.0.jar")
	contents := `{
		"schemaVersion": 1,
		"id": "prismmod",
		"name": "Prism Mod",
		"version": "2.1.0",
		"environment": "client",
		"license": ["MIT", "Apache-2.0"],
		"icon": "assets/prismmod/icon.png",
		"depends": {
			"minecraft": ">=1.20",
			"fabricloader": [">=0.14.9", "<=0.15.0"]
		},
		"suggests": {"jei": "*"},
		"entrypoints": {
			"main": ["com.example.prism.PrismMod"],
			"client": ["com.example.prism.client.PrismClient"],
			"server": ["com.example.prism.server.PrismServer"]
		}
	}`
	if err := os.WriteFile(path, zipBytes(t, map[string]string{
		"fabric.mod.json": contents,
	}), 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := scanFile(path, filepath.Base(path), true, info, PluginTypeFabric)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := plugin.Descriptors["fabric"]
	if descriptor.ID != "prismmod" || descriptor.Name != "Prism Mod" {
		t.Fatalf("unexpected fabric descriptor: %#v", descriptor)
	}
	meta := descriptor.ModMetadata
	if meta == nil {
		t.Fatal("daemon fabric parse must populate ModMetadata")
	}
	if meta.ID != "prismmod" || meta.SchemaVersion != 1 || meta.Environment != "client" {
		t.Fatalf("unexpected mod identity metadata: %#v", meta)
	}
	if meta.License != "MIT, Apache-2.0" || meta.Icon != "assets/prismmod/icon.png" {
		t.Fatalf("unexpected license/icon: %#v", meta)
	}
	expectedDepends := []ModDependency{
		{ID: "fabricloader", VersionRange: ">=0.14.9 || <=0.15.0"},
		{ID: "minecraft", VersionRange: ">=1.20"},
	}
	if len(meta.Depends) != 2 || meta.Depends[0] != expectedDepends[0] || meta.Depends[1] != expectedDepends[1] {
		t.Fatalf("unexpected depends: %#v", meta.Depends)
	}
	if len(meta.Suggests) != 1 || meta.Suggests[0].ID != "jei" {
		t.Fatalf("unexpected suggests: %#v", meta.Suggests)
	}
	if len(meta.Entrypoints) != 3 || meta.Entrypoints[0].Kind != "main" ||
		meta.Entrypoints[1].Kind != "client" || meta.Entrypoints[2].Kind != "server" {
		t.Fatalf("unexpected entrypoints: %#v", meta.Entrypoints)
	}
}

func TestPlatformDescriptorsAreSeparated(t *testing.T) {
	tests := []struct {
		name       string
		pluginType string
		descriptor string
		contents   string
		expected   string
		wrongType  string
	}{
		{
			name: "velocity", pluginType: PluginTypeVelocity,
			descriptor: "velocity-plugin.json",
			contents:   `{"id":"prism","name":"Prism","version":"1.2.0","main":"example.Main","authors":["Tester"]}`,
			expected:   "prism", wrongType: PluginTypeBungee,
		},
		{
			name: "bungee", pluginType: PluginTypeBungee,
			descriptor: "bungee.yml",
			contents:   "name: Prism\nversion: 1.2.0\nmain: example.Main\nauthor: Tester\n",
			expected:   "Prism", wrongType: PluginTypeSpigot,
		},
		{
			name: "fabric", pluginType: PluginTypeFabric,
			descriptor: "fabric.mod.json",
			contents:   `{"schemaVersion":1,"id":"prism","name":"Prism","version":"1.2.0","authors":["Tester"],"description":"fabric mod"}`,
			expected:   "Prism", wrongType: PluginTypeSpigot,
		},
		{
			name: "forge", pluginType: PluginTypeForge,
			descriptor: "META-INF/mods.toml",
			contents:   "modLoader=\"javafml\"\nloaderVersion=\"[39,)\"\n\n[[mods]]\nmodId=\"prism\"\nversion=\"1.2.0\"\ndisplayName=\"Prism\"\nauthors=\"Tester\"\ndisplayURL=\"https://example.com\"\n",
			expected:   "Prism", wrongType: PluginTypeSpigot,
		},
		{
			name: "neoforge", pluginType: PluginTypeNeoForge,
			descriptor: "META-INF/mods.toml",
			contents:   "modLoader=\"javafml\"\nloaderVersion=\"[49,)\"\n\n[[mods]]\nmodId=\"neo\"\nversion=\"1.2.0\"\ndisplayName=\"Prism\"\n",
			expected:   "Prism", wrongType: PluginTypeFabric,
		},
		{
			name: "paper", pluginType: PluginTypePaper,
			descriptor: "plugin.yml",
			contents:   "name: Prism\nversion: 1.2.0\nmain: example.Main\nauthor: Tester\n",
			expected:   "Prism", wrongType: PluginTypeVelocity,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name+".jar")
			if err := os.WriteFile(path, zipBytes(t, map[string]string{
				test.descriptor: test.contents,
			}), 0o640); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			plugin, err := scanFile(path, filepath.Base(path), true, info, test.pluginType)
			if err != nil {
				t.Fatal(err)
			}
			if plugin.PluginType != test.pluginType || plugin.Name != test.expected || plugin.Version != "1.2.0" {
				t.Fatalf("unexpected plugin descriptor: %#v", plugin)
			}
			if _, err := scanFile(path, filepath.Base(path), true, info, test.wrongType); err == nil {
				t.Fatal("expected a cross-platform descriptor mismatch")
			}
		})
	}
}
