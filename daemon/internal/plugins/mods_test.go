package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func fabricModJAR(t *testing.T, id, name, version string) []byte {
	t.Helper()
	return zipBytes(t, map[string]string{
		"fabric.mod.json": `{"schemaVersion":1,"id":"` + id + `","name":"` + name +
			`","version":"` + version + `","authors":["Tester"]}`,
	})
}

func forgeModJAR(t *testing.T, modID, name, version string) []byte {
	t.Helper()
	return zipBytes(t, map[string]string{
		"META-INF/mods.toml": "modLoader=\"javafml\"\n[[mods]]\nmodId=\"" + modID +
			"\"\nversion=\"" + version + "\"\ndisplayName=\"" + name + "\"\n",
	})
}

func TestScanModsDirectoryFindsFabricAndForgeMods(t *testing.T) {
	workspace := t.TempDir()
	modsDir := filepath.Join(workspace, "mods")
	if err := os.MkdirAll(modsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modsDir, "fabric-mod.jar"), fabricModJAR(t, "fmod", "Fabric Mod", "1.0"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modsDir, "forge-mod.jar"), forgeModJAR(t, "gmod", "Forge Mod", "2.0"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modsDir, "disabled-mod.jar.disabled"), fabricModJAR(t, "dmod", "Disabled Mod", "3.0"), 0o640); err != nil {
		t.Fatal(err)
	}

	items, warnings := newScanCache().scanMods(workspace)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	names := make(map[string]bool)
	for _, item := range items {
		names[item.Name] = item.Enabled
	}
	if !names["Fabric Mod"] || !names["Forge Mod"] {
		t.Fatalf("expected fabric and forge mods, got %#v", names)
	}
	if names["Disabled Mod"] {
		t.Fatalf("disabled mod should be reported as disabled: %#v", names)
	}

	plugins, _ := newScanCache().scan(workspace)
	if len(plugins) != 0 {
		t.Fatalf("plugins scan must not see mods directory: %#v", plugins)
	}
}

func TestModEnableDisableRenamesDisabledSuffix(t *testing.T) {
	workspace := t.TempDir()
	modsDir := filepath.Join(workspace, "mods")
	if err := os.MkdirAll(modsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	jarPath := filepath.Join(modsDir, "Example.jar")
	if err := os.WriteFile(jarPath, fabricModJAR(t, "example", "Example", "1.0"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := setModEnabled(workspace, "Example", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jarPath + ".disabled"); err != nil {
		t.Fatalf("disabled mod missing: %v", err)
	}
	if err := setModEnabled(workspace, "Example", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jarPath); err != nil {
		t.Fatalf("enabled mod missing: %v", err)
	}
	if err := uninstallMod(workspace, "Example", false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jarPath); !os.IsNotExist(err) {
		t.Fatalf("mod was not removed: %v", err)
	}
}

func TestModDriftDetectionUsesModsDirectory(t *testing.T) {
	workspace := t.TempDir()
	modsDir := filepath.Join(workspace, "mods")
	if err := os.MkdirAll(modsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	jarPath := filepath.Join(modsDir, "Example.jar")
	if err := os.WriteFile(jarPath, fabricModJAR(t, "example", "Example", "1.0"), 0o640); err != nil {
		t.Fatal(err)
	}
	baseline, err := scanEnabledModHashes(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline) != 1 {
		t.Fatalf("unexpected mod baseline: %#v", baseline)
	}
	if err := os.WriteFile(jarPath, fabricModJAR(t, "example", "Example", "1.1"), 0o640); err != nil {
		t.Fatal(err)
	}
	current, err := scanEnabledModHashes(workspace)
	if err != nil {
		t.Fatal(err)
	}
	changes := changedPluginFiles(baseline, current)
	if len(changes) != 1 {
		t.Fatalf("expected drift on mod jar, got %#v", changes)
	}
	plugins, _ := scanEnabledPluginHashes(workspace)
	if len(plugins) != 0 {
		t.Fatalf("plugin baseline must not include mods: %#v", plugins)
	}
}

func TestForgeFilenameFallbackWhenModsTOMLMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "my-awesome-mod-1.4.2.jar")
	// A plain zip without any descriptor.
	if err := os.WriteFile(path, zipBytes(t, map[string]string{"README.txt": "no descriptor"}), 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := scanFile(path, filepath.Base(path), true, info, PluginTypeForge)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.PluginType != PluginTypeForge || plugin.Name != "my-awesome-mod" || plugin.Version != "1.4.2" {
		t.Fatalf("unexpected filename fallback descriptor: %#v", plugin)
	}
}

func TestAutoDetectPrefersPluginOverModDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hybrid.jar")
	// A jar with both plugin.yml and fabric.mod.json must be detected as spigot first.
	contents := map[string]string{
		"plugin.yml":      "name: Hybrid\nversion: 1.0\nmain: example.Hybrid\n",
		"fabric.mod.json": `{"schemaVersion":1,"id":"hybrid","name":"Hybrid","version":"1.0"}`,
	}
	if err := os.WriteFile(path, zipBytes(t, contents), 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	plugin, err := scanFile(path, filepath.Base(path), true, info, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if plugin.PluginType != PluginTypeSpigot || plugin.Name != "Hybrid" {
		t.Fatalf("expected spigot priority in auto detection, got %#v", plugin)
	}
}

func TestScanModsDirectoryNotFoundIsEmpty(t *testing.T) {
	items, warnings := newScanCache().scanMods(t.TempDir())
	if len(items) != 0 || len(warnings) != 0 {
		t.Fatalf("missing mods directory should yield empty result: %#v %v", items, warnings)
	}
}
