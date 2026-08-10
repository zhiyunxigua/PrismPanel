package plugins

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

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
