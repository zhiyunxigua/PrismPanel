package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformDescriptorsAreSeparated(t *testing.T) {
	tests := []struct {
		name       string
		pluginType string
		descriptor string
		contents   string
		expected   string
	}{
		{
			name: "velocity", pluginType: PluginTypeVelocity,
			descriptor: "velocity-plugin.json",
			contents:   `{"id":"prism","name":"Prism","version":"1.2.0","main":"example.Main","authors":["Tester"]}`,
			expected:   "prism",
		},
		{
			name: "bungee", pluginType: PluginTypeBungee,
			descriptor: "bungee.yml",
			contents:   "name: Prism\nversion: 1.2.0\nmain: example.Main\nauthor: Tester\n",
			expected:   "Prism",
		},
		{
			name: "fabric", pluginType: PluginTypeFabric,
			descriptor: "fabric.mod.json",
			contents:   `{"schemaVersion":1,"id":"prism","name":"Prism","version":"1.2.0","authors":["Tester"],"description":"fabric mod"}`,
			expected:   "Prism",
		},
		{
			name: "forge", pluginType: PluginTypeForge,
			descriptor: "META-INF/mods.toml",
			contents:   "modLoader=\"javafml\"\nloaderVersion=\"[39,)\"\n\n[[mods]]\nmodId=\"prism\"\nversion=\"1.2.0\"\ndisplayName=\"Prism\"\nauthors=\"Tester\"\ndisplayURL=\"https://example.com\"\n",
			expected:   "Prism",
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
			wrongType := PluginTypeSpigot
			if test.pluginType == PluginTypeVelocity {
				wrongType = PluginTypeBungee
			}
			if _, err := scanFile(path, filepath.Base(path), true, info, wrongType); err == nil {
				t.Fatal("expected a cross-platform descriptor mismatch")
			}
		})
	}
}
