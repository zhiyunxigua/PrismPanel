package plugins

import (
	"archive/zip"
	"io"
	"os"
	"testing"
)

// TestBuildContentBundleSkipsTopLevelManifest 回归测试：内容包 zip 顶层含 manifest.yaml 时
// 打包 bundle 不得产生重复 manifest.yaml（daemon prepareBundle 对重复条目直接拒绝，
// 会导致内容包部署失败）。修复前该测试失败（bundle 出现 2 个 manifest.yaml 条目）。
func TestBuildContentBundleSkipsTopLevelManifest(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Upload(UploadInput{
		ContentZIP: testZIP(t, map[string]string{
			"manifest.yaml": "self: true\n",
			"config/a.yml":  "a: 1\n",
		}),
		ContentType: ContentTypeConfig,
		ContentName: "ManifestPack", ContentVersion: "1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 快照不应计入顶层 manifest.yaml（保留名，解压即跳过，与部署行为一致）。
	if result.Artifact.Content == nil || result.Artifact.Content.Files != 1 {
		t.Fatalf("top-level manifest.yaml must be excluded from snapshot, got %#v", result.Artifact.Content)
	}
	bundle, _, err := repository.BuildContentBundle("manifestpack", result.Artifact.ArtifactID, ContentTypeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bundle)
	reader, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	count := 0
	for _, entry := range reader.File {
		if entry.Name == "manifest.yaml" {
			count++
			if count > 1 {
				t.Fatalf("duplicate manifest.yaml entries in content bundle: %#v", reader.File)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one manifest.yaml, got %d", count)
	}
	_ = io.Discard
}
