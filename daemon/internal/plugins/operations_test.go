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

func TestPluginAndConfigDeployAreSeparate(t *testing.T) {
	workspace := t.TempDir()
	pluginDir := filepath.Join(workspace, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginDir, "Example"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "Example", "config.yml"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "Example", "extra.yml"), []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}

	oldJAR := pluginJAR(t, "Example", "1.0", "com.example.Main")
	if err := os.WriteFile(filepath.Join(pluginDir, "Example-1.0.jar"), oldJAR, 0o640); err != nil {
		t.Fatal(err)
	}
	jar := pluginJAR(t, "Example", "2.0", "com.example.Main")
	pluginBundlePath := pluginBundle(t, jar, "Example-2.0.jar", map[string]string{"config.yml": "from-plugin-bundle"})
	bundle, cleanup, err := prepareBundle(pluginBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := deployPluginToWorkspace(workspace, bundle); err != nil {
		t.Fatal(err)
	}

	installed := filepath.Join(pluginDir, "Example-1.0.jar")
	if hash, err := fileSHA256(installed); err != nil || hash != bundle.plugin.SHA256 {
		t.Fatalf("unexpected deployed jar: %s, %v", hash, err)
	}
	if data, err := os.ReadFile(filepath.Join(pluginDir, "Example", "config.yml")); err != nil || string(data) != "old" {
		t.Fatalf("plugin deployment changed config: %q, %v", data, err)
	}

	configBundlePath := configBundle(t, "Example", "2.0", map[string]string{"config.yml": "new"})
	config, configCleanup, err := prepareBundle(configBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer configCleanup()
	if err := deployConfigToWorkspace(workspace, config); err != nil {
		t.Fatal(err)
	}
	if hash, err := fileSHA256(installed); err != nil || hash != bundle.plugin.SHA256 {
		t.Fatalf("config deployment changed jar: %s, %v", hash, err)
	}
	if data, err := os.ReadFile(filepath.Join(pluginDir, "Example", "config.yml")); err != nil || string(data) != "new" {
		t.Fatalf("config was not overlaid: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(pluginDir, "Example", "extra.yml")); err != nil || string(data) != "keep" {
		t.Fatalf("extra config was not preserved: %q, %v", data, err)
	}
}

func TestConfigDeploymentFailureIsImmediate(t *testing.T) {
	workspace := t.TempDir()
	pending, err := newPendingStore(filepath.Join(t.TempDir(), "plugin-pending"))
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{pending: pending}
	result, applyErr := service.applyOrQueue(
		operationTarget{ID: "instance-1", Workspace: workspace, Running: true},
		pendingOperation{Type: "deploy_config"}, "bundle.zip",
		func() error { return os.ErrPermission },
	)
	if applyErr == nil || result.Status != "failed" {
		t.Fatalf("config deployment failure was not returned immediately: result=%#v err=%v", result, applyErr)
	}
	if _, statErr := os.Stat(filepath.Join(pending.root, "instance-1", "pending.json")); !os.IsNotExist(statErr) {
		t.Fatalf("config deployment unexpectedly queued: %v", statErr)
	}
}

func TestConfigDeploymentDoesNotRequestRestart(t *testing.T) {
	service := &Service{}
	result, err := service.applyOrQueue(
		operationTarget{ID: "instance-1", Workspace: t.TempDir(), Running: true},
		pendingOperation{Type: "deploy_config"}, "",
		func() error { return nil },
	)
	if err != nil || result.Status != "applied" || result.PendingRestart {
		t.Fatalf("config deployment unexpectedly requested restart: result=%#v err=%v", result, err)
	}
}

func configBundle(t *testing.T, name, version string, config map[string]string) string {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	manifest := "kind: config\nplugin_type: spigot\nname: " + name + "\nversion: " + version + "\nconfig:\n" +
		"  directory: " + name + "\n  present: true\n"
	entry, err := archive.Create("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, manifest); err != nil {
		t.Fatal(err)
	}
	for file, content := range config {
		entry, err := archive.Create("config/" + file)
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
	path := filepath.Join(t.TempDir(), "config.zip")
	if err := os.WriteFile(path, buffer.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

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
	if err := uninstallPlugin(workspace, "Example", false, ""); err != nil {
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

func pluginBundle(t *testing.T, jar []byte, filename string, config map[string]string) string {
	t.Helper()
	hash := sha256.Sum256(jar)
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, _ := archive.Create("plugin.jar")
	_, _ = entry.Write(jar)
	manifest := "name: Example\nversion: 2.0\nmain: com.example.Main\nartifact:\n" +
		"  original_filename: " + filename + "\n  sha256: " + hex.EncodeToString(hash[:]) + "\n" +
		"config:\n  directory: Example\n  present: true\n"
	entry, _ = archive.Create("manifest.yaml")
	_, _ = io.WriteString(entry, manifest)
	for name, content := range config {
		entry, _ = archive.Create("config/" + name)
		_, _ = io.WriteString(entry, content)
	}
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
