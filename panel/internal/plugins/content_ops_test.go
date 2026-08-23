package plugins

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// TestAddContentCreatesVersions 验证内容包独立版本化：
// AddContent 新增版本（contentID 递增）并成为 current，旧版本保留可列出。
func TestAddContentCreatesVersions(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example-1.0.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
		ContentZIP: testZIP(t, map[string]string{"config/a.yml": "a: 1\n"}),
		ContentType: ContentTypeConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	pluginID, artifactID := result.Plugin.PluginID, result.Artifact.ArtifactID
	if result.Artifact.ContentID() != 1 {
		t.Fatalf("first content version must be 1: %#v", result.Artifact.Content)
	}

	updated, err := repository.AddContent(pluginID, artifactID, ContentTypeConfig,
		testZIP(t, map[string]string{"config/b.yml": "b: 2\n", "plugins/x/c.json": `{"x":1}`}))
	if err != nil {
		t.Fatal(err)
	}
	if updated.ContentID() != 2 || updated.Content.Files != 2 || updated.Content.Type != ContentTypeConfig {
		t.Fatalf("current content must switch to version 2: %#v", updated.Content)
	}
	versions, err := repository.ListContent(pluginID, artifactID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].ContentID != 1 || versions[1].ContentID != 2 {
		t.Fatalf("unexpected versions: %#v", versions)
	}
	// 磁盘：content/1 与 content/2 并存，v1 内容保留。
	for _, relative := range []string{"config/a.yml"} {
		if _, err := os.Stat(filepath.Join(repository.Root(), "example", "1", "content", "1", filepath.FromSlash(relative))); err != nil {
			t.Fatalf("version 1 entry %s missing: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repository.Root(), "example", "1", "content", "2", "config", "b.yml")); err != nil {
		t.Fatalf("version 2 entry missing: %v", err)
	}
}

// TestDeleteContentVersion 验证删除内容包版本：删除 current 回退到剩余最高版本；
// 删除全部版本后 Manifest.Content 置空且版本目录移除。
func TestDeleteContentVersion(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example-1.0.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
		ContentZIP: testZIP(t, map[string]string{"config/a.yml": "a"}),
		ContentType: ContentTypeConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	pluginID, artifactID := result.Plugin.PluginID, result.Artifact.ArtifactID
	if _, err := repository.AddContent(pluginID, artifactID, ContentTypeConfig,
		testZIP(t, map[string]string{"config/b.yml": "b"})); err != nil {
		t.Fatal(err)
	}
	// 删除 current（版本 2）→ current 回退到版本 1。
	manifest, err := repository.DeleteContent(pluginID, artifactID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ContentID() != 1 {
		t.Fatalf("current must fall back to version 1: %#v", manifest.Content)
	}
	if _, err := os.Stat(filepath.Join(repository.Root(), "example", "1", "content", "2")); !os.IsNotExist(err) {
		t.Fatalf("version 2 directory must be removed: %v", err)
	}
	// 删除最后一个版本 → 无内容包。
	manifest, err = repository.DeleteContent(pluginID, artifactID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Content != nil {
		t.Fatalf("content must be nil after deleting last version: %#v", manifest.Content)
	}
	versions, err := repository.ListContent(pluginID, artifactID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Fatalf("no versions expected: %#v", versions)
	}
	if _, err := os.Stat(filepath.Join(repository.Root(), "example", "1", "content")); !os.IsNotExist(err) {
		t.Fatalf("content dir must be removed: %v", err)
	}
}

// TestDeleteContentVersionNotFound 验证删除不存在的版本返回 os.ErrNotExist。
func TestDeleteContentVersionNotFound(t *testing.T) {
	repository, _ := NewRepository(t.TempDir())
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeleteContent(result.Plugin.PluginID, result.Artifact.ArtifactID, 7); !os.IsNotExist(err) {
		t.Fatalf("expected os.ErrNotExist: %v", err)
	}
}

// TestDeleteArtifactVersion 验证删除制品版本：剩余最高版本成为 current；
// 删除最后一个制品时整个仓库条目被移除。
func TestDeleteArtifactVersion(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.Upload(UploadInput{
		JARFilename: "Example-1.0.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Upload(UploadInput{
		JARFilename: "Example-2.0.jar", JAR: testJAR(t, "Example", "2.0", "com.example.Main"),
	}); err != nil {
		t.Fatal(err)
	}
	pluginID := first.Plugin.PluginID
	plugin, err := repository.DeleteArtifact(pluginID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.CurrentArtifactID != 2 || len(plugin.Artifacts) != 1 || plugin.Artifacts[0].ArtifactID != 2 {
		t.Fatalf("unexpected plugin after artifact delete: %#v", plugin)
	}
	// 删除最后一个制品 → 条目整体删除。
	if _, err := repository.DeleteArtifact(pluginID, 2); !os.IsNotExist(err) {
		t.Fatalf("expected plugin entry removal: %v", err)
	}
	if _, err := repository.Get(pluginID); !os.IsNotExist(err) {
		t.Fatalf("plugin must no longer exist: %v", err)
	}
}

// TestDeletePluginEntry 验证删除整个仓库条目。
func TestDeletePluginEntry(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.Upload(UploadInput{
		JARFilename: "Alpha.jar", JAR: testJAR(t, "Alpha", "1", "com.example.Main"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Upload(UploadInput{
		JARFilename: "Beta.jar", JAR: testJAR(t, "Beta", "1", "com.example.Main"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeletePlugin(first.Plugin.PluginID); err != nil {
		t.Fatal(err)
	}
	catalog, err := repository.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 1 || catalog[0].Name != "Beta" {
		t.Fatalf("unexpected catalog after plugin delete: %#v", catalog)
	}
	if err := repository.DeletePlugin("no-such-plugin"); !os.IsNotExist(err) {
		t.Fatalf("deleting missing plugin must fail: %v", err)
	}
}

// TestDeleteAllContent 验证「仅删除内容包」：删除制品下全部内容包版本与索引。
func TestDeleteAllContent(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main"),
		ContentZIP: testZIP(t, map[string]string{"config/a.yml": "v1"}),
		ContentType: ContentTypeConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	pluginID, artifactID := result.Plugin.PluginID, result.Artifact.ArtifactID
	if _, err := repository.AddContent(pluginID, artifactID, ContentTypeConfig,
		testZIP(t, map[string]string{"config/b.yml": "v2"})); err != nil {
		t.Fatal(err)
	}
	manifest, err := repository.DeleteAllContent(pluginID, artifactID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Content != nil {
		t.Fatalf("content must be nil after delete-all: %#v", manifest.Content)
	}
	versions, err := repository.ListContent(pluginID, artifactID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Fatalf("no versions expected: %#v", versions)
	}
	if _, err := os.Stat(filepath.Join(repository.Root(), "example", "1", "content")); !os.IsNotExist(err) {
		t.Fatalf("content dir must be removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repository.Root(), "example", "1", "content.yaml")); !os.IsNotExist(err) {
		t.Fatalf("content index must be removed: %v", err)
	}
	// jar 制品不受影响。
	if _, err := os.Stat(filepath.Join(repository.Root(), "example", "1", "plugin.jar")); err != nil {
		t.Fatalf("jar must survive content delete: %v", err)
	}
}

// TestSetCurrentArtifact 验证切换 current 制品（回滚到旧版本）。
func TestSetCurrentArtifact(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.Upload(UploadInput{
		JARFilename: "Example-1.0.jar", JAR: testJAR(t, "Example", "1.0", "com.example.Main"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Upload(UploadInput{
		JARFilename: "Example-2.0.jar", JAR: testJAR(t, "Example", "2.0", "com.example.Main"),
	}); err != nil {
		t.Fatal(err)
	}
	pluginID := first.Plugin.PluginID
	plugin, err := repository.SetCurrentArtifact(pluginID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.CurrentArtifactID != 1 {
		t.Fatalf("current artifact must switch to 1: %#v", plugin)
	}
	if _, err := repository.SetCurrentArtifact(pluginID, 99); !os.IsNotExist(err) {
		t.Fatalf("switching to missing artifact must fail: %v", err)
	}
}

// TestBuildContentBundleUsesCurrentVersion 验证内容包 bundle 从 current 版本构建。
func TestBuildContentBundleUsesCurrentVersion(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main"),
		ContentZIP: testZIP(t, map[string]string{"config/a.yml": "v1"}),
		ContentType: ContentTypeConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	pluginID, artifactID := result.Plugin.PluginID, result.Artifact.ArtifactID
	if _, err := repository.AddContent(pluginID, artifactID, ContentTypeConfig,
		testZIP(t, map[string]string{"config/b.yml": "v2"})); err != nil {
		t.Fatal(err)
	}
	bundle, _, err := repository.BuildContentBundle(pluginID, artifactID, ContentTypeConfig)
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
	if !entries["config/b.yml"] {
		t.Fatalf("bundle must contain current version content: %#v", entries)
	}
	if entries["config/a.yml"] {
		t.Fatalf("bundle must not contain old version content: %#v", entries)
	}
}

// TestRescanKeepsCurrentContentVersion 验证 rescan 后 current 内容包版本保持不变。
func TestRescanKeepsCurrentContentVersion(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		JARFilename: "Example.jar", JAR: testJAR(t, "Example", "1", "com.example.Main"),
		ContentZIP: testZIP(t, map[string]string{"config/a.yml": "v1"}),
		ContentType: ContentTypeConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	pluginID, artifactID := result.Plugin.PluginID, result.Artifact.ArtifactID
	if _, err := repository.AddContent(pluginID, artifactID, ContentTypeConfig,
		testZIP(t, map[string]string{"config/b.yml": "v2"})); err != nil {
		t.Fatal(err)
	}
	report, err := repository.Rescan()
	if err != nil {
		t.Fatal(err)
	}
	if report.RebuiltManifests != 0 || report.RecoveredChanges != 0 {
		t.Fatalf("unexpected scan report: %#v", report)
	}
	manifest, _, err := repository.Artifact(pluginID, artifactID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ContentID() != 2 || manifest.Content.SHA256 == "" {
		t.Fatalf("current content version must survive rescan: %#v", manifest.Content)
	}
	versions, err := repository.ListContent(pluginID, artifactID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions must survive rescan: %#v", versions)
	}
}
