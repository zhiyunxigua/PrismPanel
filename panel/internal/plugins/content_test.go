package plugins

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestContentUploadWithJAR 验证 jar + 内容包（config 类型）上传：
// Manifest.Content 快照（类型/顶层树/SHA256/文件数/大小）与磁盘 content/ 目录。
func TestContentUploadWithJAR(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example-1.0.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
		ContentZIP: testZIP(t, map[string]string{
			"config/config.yml":          "enabled: true\n",
			"plugins/testplug/cfg.json":  `{"a":1}`,
			"README.txt":                 "content pack readme",
		}),
		ContentType: ContentTypeConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	content := result.Artifact.Content
	if content == nil || !content.Present || content.Type != ContentTypeConfig {
		t.Fatalf("unexpected content snapshot: %#v", content)
	}
	if content.Files != 3 || content.Size <= 0 || content.SHA256 == "" || content.ContentID != 1 {
		t.Fatalf("unexpected content stats: %#v", content)
	}
	top := make(map[string]string)
	for _, entry := range content.Tree {
		top[entry.Path] = entry.Type
	}
	if top["config"] != "dir" || top["plugins"] != "dir" || top["README.txt"] != "file" {
		t.Fatalf("unexpected top-level tree: %#v", content.Tree)
	}
	// 磁盘落盘：内容包版本化布局 content/<contentID>/，zip 顶层结构在版本目录内。
	contentRoot := filepath.Join(repository.Root(), "example", "1", "content", "1")
	for _, relative := range []string{"config/config.yml", "plugins/testplug/cfg.json", "README.txt"} {
		if _, err := os.Stat(filepath.Join(contentRoot, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("content entry %s missing: %v", relative, err)
		}
	}
	// 内容包版本索引存在且 current 指向版本 1。
	index, err := loadContentIndex(filepath.Join(repository.Root(), "example", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if index.Current != 1 || len(index.Versions) != 1 || index.Versions[0].ContentID != 1 {
		t.Fatalf("unexpected content index: %#v", index)
	}
	if result.Artifact.Artifact.SHA256 == "" {
		t.Fatal("jar artifact must still be recorded")
	}
}

// TestContentOnlyUploadFullWithEmbeddedJAR 验证仅内容包上传（full 类型）：
// 无 jar 参数，身份从 zip 内嵌 jar 自动推导，快照记录完整目录结构。
func TestContentOnlyUploadFullWithEmbeddedJAR(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	innerJAR := testZIP(t, map[string]string{
		"fabric.mod.json": `{"schemaVersion":1,"id":"fmod","name":"Fabric Mod","version":"2.0","authors":["Tester"]}`,
	})
	zipBytes := zipWithBinaries(t, map[string]string{
		"config/fabric.toml":   "enable = true\n",
		"world/level.dat":      "level",
	}, map[string][]byte{
		"mods/fmod-2.0.jar": innerJAR,
	})
	result, err := repository.Upload(UploadInput{
		PluginType: PluginTypeFabric, ContentZIP: zipBytes, ContentType: ContentTypeFull,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.Name != "Fabric Mod" || result.Artifact.Version != "2.0" {
		t.Fatalf("identity was not derived from embedded jar: %#v", result.Artifact)
	}
	content := result.Artifact.Content
	if content == nil || content.Type != ContentTypeFull || !content.Present {
		t.Fatalf("unexpected content snapshot: %#v", content)
	}
	top := make(map[string]string)
	for _, entry := range content.Tree {
		top[entry.Path] = entry.Type
	}
	if top["mods"] != "dir" || top["config"] != "dir" || top["world"] != "dir" {
		t.Fatalf("unexpected full pack top-level tree: %#v", content.Tree)
	}
	// 仅内容包制品：无 plugin.jar。
	if _, err := os.Stat(filepath.Join(repository.Root(), PluginTypeFabric, "fabric-mod", "1", "plugin.jar")); !os.IsNotExist(err) {
		t.Fatalf("content-only artifact must not have plugin.jar: %v", err)
	}
}

// TestContentOnlyUploadConfigRequiresIdentity 验证仅内容包（config 类型）必须提供 name/version。
func TestContentOnlyUploadConfigRequiresIdentity(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	if _, err := repository.Upload(UploadInput{
		ContentZIP:  testZIP(t, map[string]string{"config/config.yml": "a: 1\n"}),
		ContentType: ContentTypeConfig,
	}); err == nil || !strings.Contains(err.Error(), "name and version") {
		t.Fatalf("config-only upload without identity must fail: %v", err)
	}
	result, err := repository.Upload(UploadInput{
		ContentZIP: testZIP(t, map[string]string{"config/config.yml": "a: 1\n"}),
		ContentType: ContentTypeConfig,
		ContentName: "MyConfigPack", ContentVersion: "1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Artifact.Name != "MyConfigPack" || result.Artifact.Version != "1.0" ||
		result.Artifact.Content == nil || result.Artifact.Content.Type != ContentTypeConfig {
		t.Fatalf("unexpected config-only artifact: %#v", result.Artifact)
	}
}

// TestContentZIPRejectsPathEscape 验证内容包 Zip Slip 防护（`..` 逃逸；绝对路径被归一化为相对路径）。
func TestContentZIPRejectsPathEscape(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	for _, bad := range []string{"../escape.yml", "..\\evil.yml", "a/../../escape.yml"} {
		_, err := repository.Upload(UploadInput{
			JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main"),
			ContentZIP: testZIP(t, map[string]string{bad: "bad"}), ContentType: ContentTypeConfig,
		})
		if err == nil {
			t.Fatalf("expected path traversal %q to be rejected", bad)
		}
	}
}

// TestContentZIPRejectsSymlink 验证内容包拒绝符号链接条目。
func TestContentZIPRejectsSymlink(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	_, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main"),
		ContentZIP: zipWithSymlink(t, map[string]string{"config/a.yml": "a"}, "config/link"), ContentType: ContentTypeConfig,
	})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("expected symlink to be rejected: %v", err)
	}
}

// TestContentUploadDuplicate 验证内容包上传去重（同 jar + 同内容 → duplicate）。
func TestContentUploadDuplicate(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	input := UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main"),
		ContentZIP: testZIP(t, map[string]string{"config/a.yml": "a"}), ContentType: ContentTypeConfig,
	}
	first, err := repository.Upload(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.Upload(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Artifact.ArtifactID != second.Artifact.ArtifactID || !second.Duplicate {
		t.Fatalf("content upload was not deduplicated: %#v", second)
	}
}

// TestBuildContentBundleStructure 验证 BuildContentBundle 打通用结构 zip：
// 内容文件按相对结构放在 zip 根（无 config/ 前缀），manifest kind=content。
func TestBuildContentBundleStructure(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example-1.0.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
		ContentZIP: testZIP(t, map[string]string{
			"config/config.yml":         "enabled: true\n",
			"plugins/testplug/cfg.json": `{"a":1}`,
		}),
		ContentType: ContentTypeConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := repository.BuildContentBundle("example", result.Artifact.ArtifactID, ContentTypeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bundle)
	reader, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := make(map[string]bool)
	for _, entry := range reader.File {
		entries[entry.Name] = true
	}
	// 相对结构：config/ 与 plugins/ 直接在 zip 根，无额外前缀。
	if !entries["manifest.yaml"] || !entries["config/config.yml"] || !entries["plugins/testplug/cfg.json"] {
		t.Fatalf("unexpected content bundle entries: %#v", entries)
	}
	var payload struct {
		Kind    string `yaml:"kind"`
		Content struct {
			Type    string `yaml:"type"`
			Present bool   `yaml:"present"`
		} `yaml:"content"`
	}
	manifestFile, err := reader.Open("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := io.ReadAll(manifestFile)
	manifestFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(manifestData, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Kind != "content" || payload.Content.Type != ContentTypeConfig || !payload.Content.Present {
		t.Fatalf("unexpected content bundle manifest: %#v", payload)
	}
}

// TestBuildContentBundleFull 验证完全配置内容包 bundle 打包（含 world 等大目录）。
func TestBuildContentBundleFull(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		ContentZIP: testZIP(t, map[string]string{
			"mods/fmod.jar":        "not-a-real-jar",
			"config/fabric.toml":   "enable = true\n",
			"world/region/r.0.0.mca": "region-data",
		}),
		ContentType: ContentTypeFull,
		ContentName: "FullPack", ContentVersion: "1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, _, err := repository.BuildContentBundle("fullpack", result.Artifact.ArtifactID, ContentTypeFull)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bundle)
	entries := zipEntries(t, bundle)
	for _, name := range []string{"manifest.yaml", "mods/fmod.jar", "config/fabric.toml", "world/region/r.0.0.mca"} {
		if !entries[name] {
			t.Fatalf("content bundle missing %s: %#v", name, entries)
		}
	}
}

// TestContentOnlyArtifactSurvivesRescan 验证仅内容包制品在仓库扫描后内容快照保持完整。
func TestContentOnlyArtifactSurvivesRescan(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		ContentZIP: testZIP(t, map[string]string{
			"config/a.yml": "a: 1\n",
			"world/level.dat": "level",
		}),
		ContentType: ContentTypeConfig,
		ContentName: "RescanPack", ContentVersion: "1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := repository.Rescan()
	if err != nil {
		t.Fatal(err)
	}
	if report.RebuiltManifests != 0 || report.RecoveredChanges != 0 || len(report.Plugins) != 1 {
		t.Fatalf("unexpected scan report: %#v", report)
	}
	manifest, _, err := repository.Artifact(result.Plugin.PluginID, result.Artifact.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Content == nil || manifest.Content.Type != ContentTypeConfig || manifest.Content.Files != 2 {
		t.Fatalf("content snapshot lost after rescan: %#v", manifest.Content)
	}
}

func zipWithBinaries(t *testing.T, files map[string]string, binaries map[string][]byte) []byte {
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
	for name, contents := range binaries {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func zipWithSymlink(t *testing.T, files map[string]string, symlink string) []byte {
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
	header := &zip.FileHeader{Name: symlink, Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("target")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
