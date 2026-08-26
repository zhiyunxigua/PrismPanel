package game

// P2-2 下载目标路径穿越防护测试：版本 JSON 推导的 artifact.Path / maven 坐标
// 含 ../ 段时必须被拒绝（不写盘、不进计划/classpath），正常 Mojang/Fabric 路径
// （libraries/... / assets/... / versions/...）必须继续工作。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkArtifactLib(name, path, url string) mcLibrary {
	var lib mcLibrary
	lib.Name = name
	lib.Downloads.Artifact = mcDownloadArtifact{Path: path, URL: url}
	return lib
}

func mkClassifierLib(name, path, url string) mcLibrary {
	var lib mcLibrary
	lib.Name = name
	lib.Downloads.Classifiers = map[string]mcDownloadArtifact{
		nativeClassifier(): {Path: path, URL: url},
	}
	return lib
}

func writeMCVersionJSON(t *testing.T, mcDir, id, raw string) {
	t.Helper()
	path := filepath.Join(mcDir, "versions", id, id+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 越界目标一律拒绝；正常目标放行。
func TestMCTargetWithin(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "libraries", "a.jar")
	if err := mcTargetWithin(root, inside); err != nil {
		t.Fatalf("in-root target should pass: %v", err)
	}
	deep := filepath.Join(root, "versions", "natives", "x.dll")
	if err := mcTargetWithin(root, deep); err != nil {
		t.Fatalf("in-root nested target should pass: %v", err)
	}
	escape := filepath.Join(root, "libraries", "..", "..", "evil.jar")
	if err := mcTargetWithin(root, escape); err == nil || !strings.Contains(err.Error(), "版本 JSON 含非法路径") {
		t.Fatalf("out-of-root target must be rejected with 版本 JSON 含非法路径, got: %v", err)
	}
	outside := filepath.Join(filepath.Dir(root), "evil.jar")
	if err := mcTargetWithin(root, outside); err == nil {
		t.Fatal("absolute out-of-root target must be rejected")
	}
}

// fabricLibraryPathChecked：正常坐标通过，含 .. / 分隔符 / 空段的坐标拒绝。
func TestFabricLibraryPathChecked(t *testing.T) {
	valid := []struct{ name, wantSuffix string }{
		{"net.fabricmc:fabric-loader:0.16.9", "net/fabricmc/fabric-loader/0.16.9/fabric-loader-0.16.9.jar"},
		{"org.lwjgl:lwjgl-stb:3.4.1:natives-windows", "org/lwjgl/lwjgl-stb/3.4.1/lwjgl-stb-3.4.1-natives-windows.jar"},
	}
	for _, tc := range valid {
		got, err := fabricLibraryPathChecked(tc.name)
		if err != nil {
			t.Fatalf("fabricLibraryPathChecked(%q) unexpected error: %v", tc.name, err)
		}
		if slashed := filepath.ToSlash(got); !strings.HasSuffix(slashed, tc.wantSuffix) {
			t.Fatalf("fabricLibraryPathChecked(%q) = %q, want suffix %q", tc.name, got, tc.wantSuffix)
		}
	}
	invalid := []string{
		"..:artifact:1.0",             // group 点展开后含 .. / 空段
		"com.example:..:1.0",          // artifact 为 ..
		"com.example:artifact:..",     // version 为 ..
		"com.example:artifact:1.0:..", // classifier 为 ..
		"com.example:artifact:1.0:../x",
		":artifact:1.0",                    // 空 group
		"com.example::1.0",                 // 空 artifact
		"com.example:artifact:",            // 空 version
		"com.example:artifact",             // 段数不足
		"com.example/evil:artifact:1.0",    // group 含分隔符
		"com.example:artifact/evil:1.0",    // artifact 含分隔符
		"com.example:artifact:1.0:evil\\x", // classifier 含分隔符
	}
	for _, name := range invalid {
		if got, err := fabricLibraryPathChecked(name); err == nil {
			t.Fatalf("fabricLibraryPathChecked(%q) should be rejected, got %q", name, got)
		}
	}
}

// classpath 辅助：越界库路径被剔除，合法路径保留。
func TestMCLibraryPathsRejectTraversal(t *testing.T) {
	mcDir := t.TempDir()
	libs := []mcLibrary{
		mkArtifactLib("org.lwjgl:lwjgl:3.4.1", "org/lwjgl/lwjgl/3.4.1/lwjgl-3.4.1.jar", ""),
		mkArtifactLib("", "../../escape/evil.jar", ""),
	}
	paths := mcLibraryPaths(mcDir, libs)
	if len(paths) != 1 {
		t.Fatalf("expected 1 library path, got %d: %v", len(paths), paths)
	}
	want := filepath.Join(mcDir, "libraries", "org", "lwjgl", "lwjgl", "3.4.1", "lwjgl-3.4.1.jar")
	if paths[0] != want {
		t.Fatalf("unexpected library path: %s", paths[0])
	}

	fabricLibs := []mcLibrary{
		{Name: "net.fabricmc:fabric-loader:0.16.9"},
		{Name: "com.example:..:1.0"},
	}
	fpaths := fabricLibraryPaths(mcDir, fabricLibs)
	if len(fpaths) != 1 {
		t.Fatalf("expected 1 fabric library path, got %d: %v", len(fpaths), fpaths)
	}
	fwant := filepath.Join(mcDir, "libraries", "net", "fabricmc", "fabric-loader", "0.16.9", "fabric-loader-0.16.9.jar")
	if fpaths[0] != fwant {
		t.Fatalf("unexpected fabric library path: %s", fpaths[0])
	}
}

// 正常下载计划生成（含 Fabric 继承链）：client jar / 资源索引 / 库 / fabric 库
// 全部进计划；../ 逃逸的 artifact 与 maven 坐标一律不进计划。
func TestMCLaunchFilePlanRejectsTraversal(t *testing.T) {
	mcDir := t.TempDir()
	baseID := "1.21.4"
	fabricID := "fabric-loader-0.16.9-1.21.4"

	writeMCVersionJSON(t, mcDir, baseID, `{
		"id": "1.21.4",
		"downloads": {"client": {"url": "https://launcher.mojang.com/v1/objects/abc/client.jar", "size": 100, "sha1": "abc"}},
		"assetIndex": {"id": "1.21.4", "url": "https://launchermeta.mojang.com/v1/packages/def/index.json", "size": 50, "sha1": "def"},
		"libraries": [
			{"name": "org.lwjgl:lwjgl:3.4.1", "downloads": {"artifact": {"path": "org/lwjgl/lwjgl/3.4.1/lwjgl-3.4.1.jar", "url": "https://libraries.minecraft.net/org/lwjgl/lwjgl/3.4.1/lwjgl-3.4.1.jar"}}},
			{"name": "", "downloads": {"artifact": {"path": "../../escape/evil.jar", "url": "https://libraries.minecraft.net/evil.jar"}}}
		]
	}`)
	writeMCVersionJSON(t, mcDir, fabricID, `{
		"id": "fabric-loader-0.16.9-1.21.4",
		"inheritsFrom": "1.21.4",
		"mainClass": "net.fabricmc.loader.impl.launch.knot.KnotClient",
		"libraries": [
			{"name": "net.fabricmc:fabric-loader:0.16.9", "url": "https://maven.fabricmc.net/"},
			{"name": "com.example:..:1.0"}
		]
	}`)

	plan := mcLaunchFilePlan(mcDir, &mcLaunchProfile{GameID: fabricID, InheritsFrom: baseID})

	want := []string{
		filepath.Join(mcDir, "versions", baseID, baseID+".jar"),
		filepath.Join(mcDir, "assets", "indexes", baseID+".json"),
		filepath.Join(mcDir, "libraries", "org", "lwjgl", "lwjgl", "3.4.1", "lwjgl-3.4.1.jar"),
		filepath.Join(mcDir, "libraries", "net", "fabricmc", "fabric-loader", "0.16.9", "fabric-loader-0.16.9.jar"),
	}
	for _, w := range want {
		if _, ok := plan[w]; !ok {
			t.Errorf("plan missing expected path %s", w)
		}
	}
	evil := filepath.Join(filepath.Dir(mcDir), "escape", "evil.jar")
	if _, ok := plan[evil]; ok {
		t.Error("escaped artifact path must not be in plan")
	}
	for p := range plan {
		if err := mcTargetWithin(mcDir, p); err != nil {
			t.Errorf("plan contains out-of-root path %s: %v", p, err)
		}
	}
}

// 库下载：合法库正常落盘到 .minecraft/libraries，越界库拒绝并报「版本 JSON 含非法路径」。
func TestDownloadMCLibrariesRejectsTraversal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("library-bytes"))
	}))
	defer server.Close()

	os.Setenv("PRISMPANEL_MC_MIRROR", server.URL)
	defer os.Unsetenv("PRISMPANEL_MC_MIRROR")

	mcDir := t.TempDir()
	libs := []mcLibrary{
		mkArtifactLib("org.lwjgl:lwjgl:3.4.1", "org/lwjgl/lwjgl/3.4.1/lwjgl-3.4.1.jar", "https://libraries.minecraft.net/org/lwjgl/lwjgl/3.4.1/lwjgl-3.4.1.jar"),
		mkArtifactLib("", "../../escape/evil.jar", "https://libraries.minecraft.net/evil.jar"),
	}
	_, err := downloadMCLibraries(context.Background(), mcDir, libs, nil)
	if err == nil || !strings.Contains(err.Error(), "版本 JSON 含非法路径") {
		t.Fatalf("expected traversal rejection error, got: %v", err)
	}
	valid := filepath.Join(mcDir, "libraries", "org", "lwjgl", "lwjgl", "3.4.1", "lwjgl-3.4.1.jar")
	if data, rerr := os.ReadFile(valid); rerr != nil || len(data) == 0 {
		t.Fatalf("valid library should be downloaded to %s: %v", valid, rerr)
	}
	escaped := filepath.Join(filepath.Dir(mcDir), "escape", "evil.jar")
	if _, rerr := os.Stat(escaped); !os.IsNotExist(rerr) {
		t.Fatalf("escaped target must not be written: %s (err=%v)", escaped, rerr)
	}
}

// natives 下载：classifier 目标越界时在写盘前拒绝。
func TestDownloadMCNativesRejectsTraversal(t *testing.T) {
	mcDir := t.TempDir()
	libs := []mcLibrary{
		mkClassifierLib("org.lwjgl:lwjgl:3.4.1", "../../escape/natives.zip", "https://libraries.minecraft.net/natives.zip"),
	}
	err := downloadMCNatives(context.Background(), mcDir, libs, nil)
	if err == nil || !strings.Contains(err.Error(), "版本 JSON 含非法路径") {
		t.Fatalf("expected traversal rejection error, got: %v", err)
	}
	escaped := filepath.Join(filepath.Dir(mcDir), "escape", "natives.zip")
	if _, rerr := os.Stat(escaped); !os.IsNotExist(rerr) {
		t.Fatalf("escaped natives target must not be written: %s (err=%v)", escaped, rerr)
	}
}
