package game

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMCJavaComponent(t *testing.T) {
	cases := map[string]string{
		"1.8.9":                       "jre-legacy",
		"1.12.2":                      "jre-legacy",
		"1.16.5":                      "jre-legacy",
		"1.17.1":                      "java-runtime-alpha",
		"1.19.2":                      "java-runtime-gamma",
		"1.20.6":                      "java-runtime-delta",
		"1.21.4":                      "java-runtime-delta",
		"fabric-loader-0.16.9-1.21.4": "java-runtime-delta",
	}
	for version, want := range cases {
		if got := mcJavaComponent(version); got != want {
			t.Errorf("mcJavaComponent(%q) = %q, want %q", version, got, want)
		}
	}
}

// TestMCComponentForMajor 直接覆盖大版本→组件映射（含未来 majorVersion≥22 的 epsilon 前瞻分支）。
func TestMCComponentForMajor(t *testing.T) {
	cases := map[string]string{
		"8":  "jre-legacy",
		"16": "java-runtime-alpha",
		"17": "java-runtime-gamma",
		"21": "java-runtime-delta",
		"24": "java-runtime-epsilon",
		"25": "java-runtime-epsilon",
		"26": "java-runtime-epsilon",
	}
	for major, want := range cases {
		if got := mcComponentForMajor(major); got != want {
			t.Errorf("mcComponentForMajor(%q) = %q, want %q", major, got, want)
		}
	}
}

func TestFileJavaMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.bin")
	content := []byte("hello java")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(content)
	sha1Hex := hex.EncodeToString(sum[:])
	if !fileJavaMatches(path, int64(len(content)), sha1Hex) {
		t.Error("existing file should match sha1")
	}
	if fileJavaMatches(path, int64(len(content)), "0000000000000000000000000000000000000000") {
		t.Error("wrong sha1 should not match")
	}
	if fileJavaMatches(path, int64(len(content))+1, sha1Hex) {
		t.Error("wrong size should not match")
	}
}

func TestMCJavaManifestSmoke(t *testing.T) {
	os.Setenv("PRISMPANEL_MC_MIRROR", "bmclapi")
	defer os.Unsetenv("PRISMPANEL_MC_MIRROR")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var all javaRuntimeAll
	if err := getJSON(ctx, javaRuntimeManifestURL, &all); err != nil {
		t.Skipf("network unavailable: %v", err)
	}
	for _, component := range []string{"jre-legacy", "java-runtime-gamma", "java-runtime-beta"} {
		releases := all.WindowsX64[component]
		if len(releases) == 0 || releases[0].Manifest.URL == "" {
			t.Errorf("component %s missing on windows-x64", component)
		}
	}
}
