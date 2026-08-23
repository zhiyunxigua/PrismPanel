package winappupdates

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestRepositoryPublishesAndOrdersReleases(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := testBundle(t, "0.0.1", []byte("MZ-first"))
	release, err := repository.Publish(bytes.NewReader(first), int64(len(first)), "first release", Uploader{Username: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "0.0.1" || release.Notes != "first release" {
		t.Fatalf("unexpected release: %#v", release)
	}
	second := testBundle(t, "0.1.0", []byte("MZ-second"))
	if _, err := repository.Publish(bytes.NewReader(second), int64(len(second)), "second", Uploader{}); err != nil {
		t.Fatal(err)
	}
	latest, err := repository.Latest()
	if err != nil || latest.Version != "0.1.0" {
		t.Fatalf("latest release = %#v, %v", latest, err)
	}
	releases, err := repository.List()
	if err != nil || len(releases) != 2 || releases[0].Version != "0.1.0" {
		t.Fatalf("release list = %#v, %v", releases, err)
	}
	if _, _, err := repository.Artifact("0.0.1"); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryRejectsNonIncreasingAndTamperedReleases(t *testing.T) {
	repository, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bundle := testBundle(t, "1.0.0", []byte("MZ-valid"))
	if _, err := repository.Publish(bytes.NewReader(bundle), int64(len(bundle)), "", Uploader{}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Publish(bytes.NewReader(bundle), int64(len(bundle)), "", Uploader{}); err == nil {
		t.Fatal("duplicate release version was accepted")
	}
	tampered := testBundleWithHash(t, "1.0.1", []byte("MZ-tampered"), "0000000000000000000000000000000000000000000000000000000000000000")
	if _, err := repository.Publish(bytes.NewReader(tampered), int64(len(tampered)), "", Uploader{}); err == nil {
		t.Fatal("tampered release was accepted")
	}
}

func TestCompareVersions(t *testing.T) {
	if CompareVersions("0.0.1", "0.0.1") != 0 || CompareVersions("0.10.0", "0.2.9") <= 0 ||
		CompareVersions("0.0.1", "dev") <= 0 {
		t.Fatal("semantic version comparison failed")
	}
}

func testBundle(t *testing.T, version string, executable []byte) []byte {
	t.Helper()
	hash := sha256.Sum256(executable)
	return testBundleWithHash(t, version, executable, hex.EncodeToString(hash[:]))
}

func testBundleWithHash(t *testing.T, version string, executable []byte, hash string) []byte {
	t.Helper()
	manifest := BuildManifest{
		SchemaVersion: 1, Product: "PrismPanel", Platform: "windows", Arch: "amd64",
		Version: version, File: "PrismPanel.exe", Size: int64(len(executable)), SHA256: hash,
		BuiltAt: time.Now().UTC(),
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, contents := range map[string][]byte{"manifest.json": manifestJSON, "PrismPanel.exe": executable} {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
