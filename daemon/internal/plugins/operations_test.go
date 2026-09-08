package plugins

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPluginEnableDisableAndUninstall(t *testing.T) {
	workspace := t.TempDir()
	pluginDir := filepath.Join(workspace, "plugins")
	configDir := filepath.Join(pluginDir, "Example")
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		t.Fatal(err)
	}
	jarPath := filepath.Join(pluginDir, "Example.jar")
	if err := os.WriteFile(jarPath, pluginJAR(t, "Example", "1.0", "com.example.Main"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := setPluginEnabled(workspace, "Example", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jarPath + ".disabled"); err != nil {
		t.Fatalf("disabled jar missing: %v", err)
	}
	if err := setPluginEnabled(workspace, "Example", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jarPath); err != nil {
		t.Fatalf("enabled jar missing: %v", err)
	}
	if err := uninstallPlugin(workspace, "Example"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jarPath); !os.IsNotExist(err) {
		t.Fatalf("jar was not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "config.yml")); err != nil {
		t.Fatalf("config should remain: %v", err)
	}
}

func pluginJAR(t *testing.T, name, version, main string) []byte {
	t.Helper()
	return zipBytes(t, map[string]string{
		"plugin.yml": "name: " + name + "\nversion: " + version + "\nmain: " + main + "\nauthors: [Tester]\n",
	})
}

func pluginBundle(t *testing.T, jar []byte, filename string, _ map[string]string) string {
	t.Helper()
	hash := sha256.Sum256(jar)
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, _ := archive.Create("plugin.jar")
	_, _ = entry.Write(jar)
	manifest := "name: Example\nversion: 2.0\nmain: com.example.Main\nartifact:\n" +
		"  original_filename: " + filename + "\n  sha256: " + hex.EncodeToString(hash[:]) + "\n"
	entry, _ = archive.Create("manifest.yaml")
	_, _ = io.WriteString(entry, manifest)
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bundle.zip")
	if err := os.WriteFile(path, buffer.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
