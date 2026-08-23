package game

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// TestMcRulesAllowDemoFeature 验证 rules 求值支持 features.is_demo_user：
// 启动器永不启动试玩账号，要求 is_demo_user=true 的规则（26.2+ 的 "--demo" 参数规则）
// 必须不匹配，否则 --demo 会被传进游戏导致离线模式进入试玩。
func TestMcRulesAllowDemoFeature(t *testing.T) {
	demoTrue := func(v bool) *bool { return &v }
	mkRule := func(action string, demo *bool) mcRule {
		r := mcRule{Action: action}
		if demo != nil {
			r.Features = &struct {
				IsDemoUser *bool `json:"is_demo_user"`
			}{IsDemoUser: demo}
		}
		return r
	}
	// is_demo_user=true → 不匹配（离线/正版用户都不是 demo 用户）
	if mcRulesAllow([]mcRule{mkRule("allow", demoTrue(true))}) {
		t.Fatal("rule requiring is_demo_user=true must not match")
	}
	// 无 features → 匹配
	if !mcRulesAllow([]mcRule{mkRule("allow", nil)}) {
		t.Fatal("plain allow rule must match")
	}
	// is_demo_user=false → 匹配
	if !mcRulesAllow([]mcRule{mkRule("allow", demoTrue(false))}) {
		t.Fatal("rule requiring is_demo_user=false must match")
	}
	// 混合：demo 专用 disallow 不影响普通 allow
	if !mcRulesAllow([]mcRule{mkRule("allow", nil), mkRule("disallow", demoTrue(true))}) {
		t.Fatal("mixed rules must still allow via plain allow rule")
	}
}

// TestBuildMCLaunchArgsDropsDemo 验证最终启动参数绝不含 --demo（无论来源是纯字符串
// 还是规则误判），否则离线模式会进入试玩、无法进多人游戏。
func TestBuildMCLaunchArgsDropsDemo(t *testing.T) {
	profile := &mcLaunchProfile{
		GameID: "26.2",
		// 同时包含纯字符串 --demo 与规则对象形态的 demo 参数（filterMCArguments 已处理规则，
		// 这里直接验证 BuildMCLaunchArgs 的最终防御剔除）
		GameArguments: []string{"--username", "${auth_player_name}", "--demo", "--version", "${version_name}", "--accessToken", "${auth_access_token}", "--userType", "${user_type}"},
		JVMArguments:  []string{"-cp", "${classpath}"},
		LibraryPaths:  []string{"a.jar"},
		NativesDir:    t.TempDir(),
		ClientJar:     "c.jar",
		AssetIndexID:  "26.2",
		MainClass:     "net.minecraft.client.main.Main",
	}
	account := MCAccount{Mode: MCAuthOffline, Name: "Steve", UUID: offlineUUID("Steve"), AccessToken: "token"}
	args, err := BuildMCLaunchArgs(profile, account, MCLaunchRequest{}, t.TempDir(), nil, 21)
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range args {
		if arg == "--demo" {
			t.Fatalf("--demo leaked into launch args: %v", args)
		}
	}
	// 关键会话参数必须存在（离线）
	joined := strings.Join(args, " ")
	for _, want := range []string{"--userType", "legacy", "--accessToken", "--uuid"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing expected launch arg %q in: %v", want, args)
		}
	}
}

// profile 提供的 url 若是基地址（https://maven.fabricmc.net/），必须按坐标补全 jar 路径，
// 否则下载会退化成请求镜像根目录（.../maven/ → 404）。
func TestFabricLibraryURLBaseAddress(t *testing.T) {
	name := "org.ow2.asm:asm-util:9.10.1"
	want := "https://maven.fabricmc.net/org/ow2/asm/asm-util/9.10.1/asm-util-9.10.1.jar"

	// 基地址（带尾斜杠）
	if got := fabricLibraryURL(name, "https://maven.fabricmc.net/"); got != want {
		t.Fatalf("base url with slash: got %q want %q", got, want)
	}
	// 基地址（无尾斜杠）
	if got := fabricLibraryURL(name, "https://maven.fabricmc.net"); got != want {
		t.Fatalf("base url without slash: got %q want %q", got, want)
	}
	// 无 url：坐标兜底
	if got := fabricLibraryURL(name, ""); got != want {
		t.Fatalf("no url fallback: got %q want %q", got, want)
	}
	// 完整 jar url：原样保留
	full := "https://maven.fabricmc.net/org/ow2/asm/asm-util/9.10.1/asm-util-9.10.1.jar"
	if got := fabricLibraryURL(name, full); got != full {
		t.Fatalf("full url should be unchanged: got %q", got)
	}
}

// 只允许当前平台+架构的变体，其余（如 x64 机器上的 arm64/x86 变体）一律跳过。
func TestMCNativesVariantAllowed(t *testing.T) {
	// 当前平台应匹配的变体
	var currentSuffix string
	switch runtime.GOOS {
	case "windows":
		switch runtime.GOARCH {
		case "arm64":
			currentSuffix = "windows-arm64"
		case "386":
			currentSuffix = "windows-x86"
		default:
			currentSuffix = "windows"
		}
	case "linux":
		if runtime.GOARCH == "arm64" {
			currentSuffix = "linux-arm64"
		} else {
			currentSuffix = "linux"
		}
	case "darwin":
		if runtime.GOARCH == "arm64" {
			currentSuffix = "macos-arm64"
		} else {
			currentSuffix = "macos"
		}
	default:
		t.Skip("unsupported test platform")
	}

	allowed := mcNativesVariantAllowed("org.lwjgl:lwjgl-stb:3.4.1:natives-" + currentSuffix)
	if !allowed {
		t.Fatalf("expected current-platform natives variant %q to be allowed", currentSuffix)
	}

	// 非 natives 条目必须放行
	if !mcNativesVariantAllowed("org.lwjgl:lwjgl-stb:3.4.1") {
		t.Fatal("non-natives entry must be allowed")
	}

	// 与当前平台不同的变体必须全部拒绝（构造所有候选变体逐一检查）
	variants := []string{"windows", "windows-arm64", "windows-x86", "linux", "linux-arm64", "macos", "macos-arm64", "osx", "osx-arm64"}
	for _, variant := range variants {
		if variant == currentSuffix {
			continue
		}
		if mcNativesVariantAllowed("org.lwjgl:lwjgl-stb:3.4.1:natives-" + variant) {
			t.Fatalf("expected non-current variant %q to be rejected on %s/%s", variant, runtime.GOOS, runtime.GOARCH)
		}
	}
}

// 资源索引的 obj.Hash（sha1）构造下载 URL 与磁盘路径，而不是 map key
// （资源路径，如 "minecraft/sounds/.../x.ogg"）。此前用 map key 导致
// 所有资源 URL 形如 .../mi/minecraft/sounds/... 全部 404。
//
// 用本地 httptest 服务器作为镜像源（PRISMPANEL_MC_MIRROR 强制镜像模式），
// 服务器记录收到的路径并校验其形态为 /assets/<hash[:2]>/<hash>。
func TestDownloadMCAssetsUsesHashNotKey(t *testing.T) {
	// 两个已知 (内容, sha1) 对
	contentByHash := map[string]string{
		"aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d": "hello", // sha1("hello")
		"7c211433f02071597741e6ff5a8ea34789abbf43": "world", // sha1("world")
	}
	var (
		mu       sync.Mutex
		requests []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		// 期望路径 /assets/<hash[:2]>/<hash>
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/assets/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		hash := parts[1]
		content, ok := contentByHash[hash]
		if !ok || !strings.HasPrefix(hash, parts[0]) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(content))
	}))
	defer server.Close()

	os.Setenv("PRISMPANEL_MC_MIRROR", server.URL)
	defer os.Unsetenv("PRISMPANEL_MC_MIRROR")

	mcDir := t.TempDir()
	indexPath := filepath.Join(mcDir, "index.json")
	// 资源索引：key 是带路径的资源名，hash 是真正的 sha1
	index := map[string]struct {
		Hash string `json:"hash"`
		Size int64  `json:"size"`
	}{
		"minecraft/sounds/mob/zombie/woodbreak.ogg": {Hash: "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d", Size: 5},
		"icons/icon.png":                            {Hash: "7c211433f02071597741e6ff5a8ea34789abbf43", Size: 5},
	}
	raw, err := json.Marshal(map[string]any{"objects": index})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := downloadMCAssets(context.Background(), mcDir, indexPath, nil); err != nil {
		t.Fatalf("downloadMCAssets failed: %v", err)
	}

	// 磁盘路径必须按 sha1 存放
	for hash := range contentByHash {
		target := filepath.Join(mcDir, "assets", "objects", hash[:2], hash)
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("expected asset file at %s (uses obj.Hash): %v", target, err)
		}
		if string(data) != contentByHash[hash] {
			t.Fatalf("content mismatch at %s", target)
		}
	}

	// 服务器收到的请求必须是 <hash[:2]>/<hash> 形态，绝不能是资源路径
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected 2 asset requests, got %v", requests)
	}
	for _, path := range requests {
		for _, bad := range []string{"minecraft/sounds", "icons/", "woodbreak"} {
			if strings.Contains(path, bad) {
				t.Fatalf("request path uses resource key instead of sha1: %s", path)
			}
		}
		if !strings.HasPrefix(path, "/assets/") {
			t.Fatalf("unexpected request path: %s", path)
		}
	}
}
