package plugins

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeployContentMapsRelativeStructure 验证内容包按相对结构部署：
// config/→config/、plugins/testplug/config.json→plugins/testplug/config.json；
// 覆盖同名 + 保留额外（不删除目标额外文件）。
func TestDeployContentMapsRelativeStructure(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "plugins", "testplug"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "plugins", "testplug", "config.json"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "plugins", "testplug", "extra.json"), []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}

	bundlePath := contentBundle(t, "TestPack", "1.0", "config", map[string]string{
		"config/foo.yml":             "foo: true\n",
		"plugins/testplug/config.json": `{"new":1}`,
	})
	bundle, cleanup, err := prepareBundle(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	stats, err := deployContentToWorkspace(workspace, bundle, deployContentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Applied != 2 || stats.Overwritten != 1 || stats.Added != 1 {
		t.Fatalf("unexpected deploy stats: %#v", stats)
	}
	checkFile(t, filepath.Join(workspace, "config", "foo.yml"), "foo: true\n")
	checkFile(t, filepath.Join(workspace, "plugins", "testplug", "config.json"), `{"new":1}`)
	checkFile(t, filepath.Join(workspace, "plugins", "testplug", "extra.json"), "keep")
}

// TestDeployContentFullWithWorld 验证完全配置（含 world 大目录）一次部署。
func TestDeployContentFullWithWorld(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"config/fabric.toml":        "enable = true\n",
		"mods/fmod.jar":             "fake-jar",
		"world/level.dat":           "level-data",
		"world/region/r.0.0.mca":    "region-00",
		"world/region/r.0.1.mca":    "region-01",
		"world/playerdata/uuid.dat": "player-data",
	}
	bundlePath := contentBundle(t, "FullPack", "1.0", "full", files)
	bundle, cleanup, err := prepareBundle(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	stats, err := deployContentToWorkspace(workspace, bundle, deployContentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Applied != len(files) || stats.Added != len(files) {
		t.Fatalf("unexpected deploy stats: %#v", stats)
	}
	for name := range files {
		if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(name))); err != nil {
			t.Fatalf("deployed entry %s missing: %v", name, err)
		}
	}
}

// TestDeployContentRollsBackOnFailure 验证事务式回滚：
// 目标 "config" 是文件而内容包要放 config/ 目录 → MkdirAll 失败，
// 此前已覆盖的 a.txt 必须恢复到旧内容，且无事务残留文件。
func TestDeployContentRollsBackOnFailure(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("old-a"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "config"), []byte("i-am-a-file"), 0o640); err != nil {
		t.Fatal(err)
	}
	bundlePath := contentBundle(t, "TestPack", "1.0", "config", map[string]string{
		"a.txt":        "new-a",
		"config/x.txt": "x",
	})
	bundle, cleanup, err := prepareBundle(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := deployContentToWorkspace(workspace, bundle, deployContentOptions{}); err == nil {
		t.Fatal("expected deployment to fail")
	}
	checkFile(t, filepath.Join(workspace, "a.txt"), "old-a")
	checkFile(t, filepath.Join(workspace, "config"), "i-am-a-file")
	assertNoTransactionFiles(t, workspace)
}

// TestDeployContentSnapshotBackup 验证完全配置部署前的整目录快照备份。
func TestDeployContentSnapshotBackup(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "existing.txt"), []byte("before"), 0o640); err != nil {
		t.Fatal(err)
	}
	backupDir := t.TempDir()
	bundlePath := contentBundle(t, "FullPack", "1.0", "full", map[string]string{
		"config/a.yml": "a",
		"world/dim1/x.dat": "x",
	})
	bundle, cleanup, err := prepareBundle(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	stats, err := deployContentToWorkspace(workspace, bundle, deployContentOptions{BackupSnapshot: true, BackupDir: backupDir})
	if err != nil {
		t.Fatal(err)
	}
	if stats.BackupPath == "" {
		t.Fatal("backup path must be reported")
	}
	reader, err := zip.OpenReader(stats.BackupPath)
	if err != nil {
		t.Fatalf("snapshot backup is not a valid zip: %v", err)
	}
	defer reader.Close()
	names := make(map[string]bool)
	for _, entry := range reader.File {
		names[entry.Name] = true
	}
	// 快照是部署前状态：包含已有文件，不含本次部署写入的新内容。
	if !names["existing.txt"] {
		t.Fatalf("snapshot backup must contain pre-deploy files: %#v", names)
	}
	if names["config/a.yml"] || names["world/dim1/x.dat"] {
		t.Fatalf("snapshot backup must not contain deployed content: %#v", names)
	}
	// 部署后工作目录包含新内容，快照文件保留供回滚。
	checkFile(t, filepath.Join(workspace, "config", "a.yml"), "a")
	if _, err := os.Stat(stats.BackupPath); err != nil {
		t.Fatalf("backup file must be retained: %v", err)
	}
}

// TestPrepareContentBundleRejectsEscape 验证 daemon 端内容包防逃逸。
func TestPrepareContentBundleRejectsEscape(t *testing.T) {
	path := contentBundleRaw(t, "kind: content\nplugin_type: spigot\nname: Evil\nversion: 1.0\ncontent:\n  type: full\n  present: true\n",
		map[string]string{"../escape.txt": "bad"})
	if _, cleanup, err := prepareBundle(path); err == nil {
		cleanup()
		t.Fatal("expected path escape to be rejected")
	}
}

// TestPrepareContentBundleRejectsUnknownType 验证内容包类型必须是 config|full。
func TestPrepareContentBundleRejectsUnknownType(t *testing.T) {
	path := contentBundleRaw(t, "kind: content\nplugin_type: spigot\nname: Bad\nversion: 1.0\ncontent:\n  type: weird\n  present: true\n",
		map[string]string{"a.txt": "a"})
	if _, cleanup, err := prepareBundle(path); err == nil {
		cleanup()
		t.Fatal("expected unknown content type to be rejected")
	}
}

func contentBundle(t *testing.T, name, version, contentType string, files map[string]string) string {
	t.Helper()
	manifest := "kind: content\nplugin_type: spigot\nname: " + name + "\nversion: " + version + "\ncontent:\n" +
		"  type: " + contentType + "\n  present: true\n"
	return contentBundleRaw(t, manifest, files)
}

func contentBundleRaw(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, manifest); err != nil {
		t.Fatal(err)
	}
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
	path := filepath.Join(t.TempDir(), "content.zip")
	if err := os.WriteFile(path, buffer.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func checkFile(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != expected {
		t.Fatalf("unexpected content at %s: %q", path, data)
	}
}

func assertNoTransactionFiles(t *testing.T, workspace string) {
	t.Helper()
	err := filepath.WalkDir(workspace, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.Contains(entry.Name(), ".prism-content-") {
			t.Fatalf("transaction file left behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
