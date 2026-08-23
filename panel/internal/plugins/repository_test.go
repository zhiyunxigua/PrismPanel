package plugins

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRepositoryUploadsFabricAndForgeMods(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fabric, err := repository.Upload(UploadInput{
		PluginType: PluginTypeFabric, JARFilename: "FabricMod-1.0.jar",
		JAR: testZIP(t, map[string]string{
			"fabric.mod.json": `{"schemaVersion":1,"id":"fmod","name":"Fabric Mod","version":"1.0","authors":["Tester"]}`,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fabric.Plugin.PluginType != PluginTypeFabric || fabric.Artifact.Name != "Fabric Mod" {
		t.Fatalf("unexpected fabric artifact: %#v", fabric)
	}
	forge, err := repository.Upload(UploadInput{
		PluginType: PluginTypeForge, JARFilename: "ForgeMod-2.0.jar",
		JAR: testZIP(t, map[string]string{
			"META-INF/mods.toml": "modLoader=\"javafml\"\n[[mods]]\nmodId=\"gmod\"\nversion=\"2.0\"\ndisplayName=\"Forge Mod\"\n",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if forge.Plugin.PluginType != PluginTypeForge || forge.Artifact.Name != "Forge Mod" {
		t.Fatalf("unexpected forge artifact: %#v", forge)
	}
	// Forge 文件名回退：没有 mods.toml 时从文件名推导。
	fallback, err := repository.Upload(UploadInput{
		PluginType: PluginTypeForge, JARFilename: "my-mod-3.1.0.jar",
		JAR: testZIP(t, map[string]string{"README.txt": "no toml"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fallback.Artifact.Name != "my-mod" || fallback.Artifact.Version != "3.1.0" {
		t.Fatalf("unexpected filename fallback artifact: %#v", fallback)
	}
	catalog, err := repository.List()
	if err != nil {
		t.Fatal(err)
	}
	types := make(map[string]bool)
	for _, plugin := range catalog {
		types[plugin.PluginType] = true
	}
	if !types[PluginTypeFabric] || !types[PluginTypeForge] {
		t.Fatalf("repository list must include fabric and forge mods: %#v", types)
	}
	// 打包 fabric mod 制品，确认 manifest 携带 plugin_type。
	bundle, _, err := repository.BuildBundle(fabric.Plugin.PluginID, fabric.Artifact.ArtifactID, PluginTypeFabric)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bundle)
	manifest, _, err := repository.Artifact(fabric.Plugin.PluginID, fabric.Artifact.ArtifactID, PluginTypeFabric)
	if err != nil || manifest.PluginType != PluginTypeFabric {
		t.Fatalf("unexpected fabric manifest: %#v, %v", manifest, err)
	}
}

func TestRepositoryUploadAndInheritConfig(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.Upload(UploadInput{
		JARFilename: "Example-1.0.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
		ConfigZIP: testZIP(t, map[string]string{"config.yml": "enabled: true\n"}),
		Uploader:  Uploader{Username: "admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Artifact.ArtifactID != 1 || !first.Artifact.Config.Present || first.Artifact.Config.Inherited {
		t.Fatalf("unexpected first artifact: %#v", first.Artifact)
	}
	second, err := repository.Upload(UploadInput{
		JARFilename: "Example-2.0.jar", JAR: testJAR(t, "Example", "2.0", "com.example.Main"),
		Uploader: Uploader{Username: "admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Artifact.ArtifactID != 2 || !second.Artifact.Config.Present || !second.Artifact.Config.Inherited {
		t.Fatalf("unexpected inherited artifact: %#v", second.Artifact)
	}
	configPath := filepath.Join(repository.Root(), "example", "2", "config", "config.yml")
	if contents, err := os.ReadFile(configPath); err != nil || string(contents) != "enabled: true\n" {
		t.Fatalf("unexpected inherited config: %q, %v", contents, err)
	}
}

func TestRepositoryDeduplicatesSameArtifact(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	input := UploadInput{JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main")}
	first, err := repository.Upload(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.Upload(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Artifact.ArtifactID != second.Artifact.ArtifactID || !second.Duplicate {
		t.Fatalf("artifact was not deduplicated: %#v", second)
	}
}

func TestConfigZIPRejectsPathEscape(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	_, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main"),
		ConfigZIP: testZIP(t, map[string]string{"../escape.yml": "bad"}),
	})
	if err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestRepositoryUpdatesConfigWithoutChangingPluginArtifact(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
		ConfigZIP: testZIP(t, map[string]string{"config.yml": "enabled: true\n"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	jarPath := filepath.Join(repository.Root(), "example", "1", "plugin.jar")
	jarBefore, err := os.ReadFile(jarPath)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repository.UpdateConfig("example", result.Artifact.ArtifactID, "config.yml", []byte("enabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if updated.ArtifactID != result.Artifact.ArtifactID || updated.Artifact.SHA256 != result.Artifact.Artifact.SHA256 {
		t.Fatalf("plugin artifact changed after config update: %#v", updated)
	}
	contents, err := repository.ReadConfig("example", result.Artifact.ArtifactID, "config.yml")
	if err != nil || string(contents) != "enabled: false\n" {
		t.Fatalf("unexpected updated config: %q, %v", contents, err)
	}
	jarAfter, err := os.ReadFile(jarPath)
	if err != nil || string(jarAfter) != string(jarBefore) {
		t.Fatalf("plugin jar changed after config update: %v", err)
	}
}

func TestRepositoryBuildsSeparatePluginAndConfigBundles(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
		ConfigZIP: testZIP(t, map[string]string{"config.yml": "enabled: true\n"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	pluginBundle, _, err := repository.BuildBundle("example", result.Artifact.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pluginBundle)
	if entries := zipEntries(t, pluginBundle); len(entries) != 2 || entries["plugin.jar"] == false || entries["manifest.yaml"] == false {
		t.Fatalf("unexpected plugin bundle entries: %#v", entries)
	}
	configBundle, _, err := repository.BuildConfigBundle("example", result.Artifact.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(configBundle)
	if entries := zipEntries(t, configBundle); len(entries) != 2 || entries["plugin.jar"] || !entries["manifest.yaml"] || !entries["config/config.yml"] {
		t.Fatalf("unexpected config bundle entries: %#v", entries)
	}
}

func TestRepositoryRescanImportsLocalJAR(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	jarPath := filepath.Join(repository.Root(), "import", "Example.jar")
	if err := os.WriteFile(jarPath, testJAR(t, "Example", "1.0", "com.example.Main"), 0o640); err != nil {
		t.Fatal(err)
	}
	report, err := repository.Rescan()
	if err != nil {
		t.Fatal(err)
	}
	if report.Imported != 1 || len(report.Plugins) != 1 || report.Plugins[0].Name != "Example" {
		t.Fatalf("unexpected scan report: %#v", report)
	}
	if _, err := os.Stat(jarPath + ".imported"); err != nil {
		t.Fatalf("import source was not retained: %v", err)
	}
}

func TestRepositoryRescanRebuildsMissingManifest(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(repository.Root(), "example", "1", "manifest.yaml")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	report, err := repository.Rescan()
	if err != nil {
		t.Fatal(err)
	}
	if report.RebuiltManifests != 1 || len(report.Plugins) != 1 ||
		report.Plugins[0].CurrentArtifactID != result.Artifact.ArtifactID {
		t.Fatalf("manifest was not rebuilt: %#v", report)
	}
}

func TestRepositoryRescanVersionsManualReplacement(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	if _, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
	}); err != nil {
		t.Fatal(err)
	}
	artifactDir := filepath.Join(repository.Root(), "example", "1")
	if err := os.WriteFile(filepath.Join(artifactDir, "plugin.jar"),
		testJAR(t, "Example", "2.0", "com.example.Main"), 0o640); err != nil {
		t.Fatal(err)
	}
	report, err := repository.Rescan()
	if err != nil {
		t.Fatal(err)
	}
	if report.RecoveredChanges != 1 || len(report.Plugins) != 1 ||
		report.Plugins[0].CurrentArtifactID != 2 || report.Plugins[0].Artifacts[0].Version != "2.0" {
		t.Fatalf("manual replacement was not versioned: %#v", report)
	}
	if _, err := os.Stat(artifactDir + ".stale"); err != nil {
		t.Fatalf("modified source was not archived: %v", err)
	}
}

func TestRepositoryPersistsAndReadsFabricModMetadata(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	iconBytes := []byte("\x89PNG\r\n\x1a\nfabric-mod-icon-payload")
	result, err := repository.Upload(UploadInput{
		PluginType: PluginTypeFabric, JARFilename: "PrismMod-2.1.0.jar",
		JAR: testZIP(t, map[string]string{
			"fabric.mod.json":          fullFabricModJSON,
			"assets/prismmod/icon.png": string(iconBytes),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := result.Artifact.ModMetadata
	if meta == nil || meta.ID != "prismmod" || meta.Environment != "client" {
		t.Fatalf("upload must carry fabric mod metadata: %#v", result.Artifact.ModMetadata)
	}
	if len(meta.Depends) != 3 || meta.Depends[1].ID != "fabricloader" ||
		meta.Depends[1].VersionRange != ">=0.14.9 || <=0.15.0" {
		t.Fatalf("unexpected persisted depends: %#v", meta.Depends)
	}

	// 从仓库读回：Artifact 与 List 都应携带相同元数据。
	manifest, _, err := repository.Artifact(result.Plugin.PluginID, result.Artifact.ArtifactID, PluginTypeFabric)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ModMetadata == nil || manifest.ModMetadata.ID != "prismmod" {
		t.Fatalf("artifact read back lost mod metadata: %#v", manifest.ModMetadata)
	}
	catalog, err := repository.List()
	if err != nil {
		t.Fatal(err)
	}
	var found *Plugin
	for index := range catalog {
		if catalog[index].PluginID == result.Plugin.PluginID && catalog[index].PluginType == PluginTypeFabric {
			found = &catalog[index]
			break
		}
	}
	if found == nil || len(found.Artifacts) != 1 || found.Artifacts[0].ModMetadata == nil ||
		found.Artifacts[0].ModMetadata.License != "MIT, Apache-2.0" {
		t.Fatalf("catalog lost fabric mod metadata: %#v", found)
	}

	// 图标提取：路径来自 fabric.mod.json icon 字段。
	icon, contentType, err := repository.Icon(result.Plugin.PluginID, result.Artifact.ArtifactID, PluginTypeFabric)
	if err != nil {
		t.Fatal(err)
	}
	if string(icon) != string(iconBytes) || contentType != "image/png" {
		t.Fatalf("unexpected icon extraction: %q (%d bytes) %q", icon, len(icon), contentType)
	}
}

func TestRepositoryIconFallbackReparsesJar(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		PluginType: PluginTypeFabric, JARFilename: "OldMod-1.0.jar",
		JAR: testZIP(t, map[string]string{
			"fabric.mod.json":       `{"schemaVersion":1,"id":"oldmod","name":"Old Mod","version":"1.0","icon":"assets/oldmod/old.png"}`,
			"assets/oldmod/old.png": "old-icon-bytes",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟老 manifest：抹掉持久化的元数据，验证回退重新解析 jar。
	manifestPath := filepath.Join(repository.root, PluginTypeFabric, result.Plugin.PluginID,
		strconv.FormatInt(result.Artifact.ArtifactID, 10), "manifest.yaml")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	report, err := repository.Rescan()
	if err != nil || report.RebuiltManifests != 1 {
		t.Fatalf("rescan should rebuild manifest: %#v, %v", report, err)
	}
	icon, _, err := repository.Icon(result.Plugin.PluginID, result.Artifact.ArtifactID, PluginTypeFabric)
	if err != nil {
		t.Fatal(err)
	}
	if string(icon) != "old-icon-bytes" {
		t.Fatalf("unexpected fallback icon: %q", icon)
	}
}

func testJAR(t *testing.T, name, version, main string) []byte {
	t.Helper()
	return testZIP(t, map[string]string{
		"plugin.yml": "name: " + name + "\nversion: " + version + "\nmain: " + main + "\nauthors: [Tester]\n",
	})
}

func testZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, contents := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func zipEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := make(map[string]bool, len(reader.File))
	for _, entry := range reader.File {
		entries[entry.Name] = true
	}
	return entries
}
