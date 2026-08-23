package game

import (
	"os"
	"strings"
	"testing"
)

// resetMCHostHealth 清空下载源健康记忆，避免测试间的跨用例状态泄漏
// （前序测试/真实网络尝试可能把镜像主机标记失败，导致候选源被过滤）。
func resetMCHostHealth() {
	mcHostHealthMu.Lock()
	mcHostHealth = map[string]mcHostState{}
	mcHostHealthMu.Unlock()
}

func TestMCCandidateURLsAuto(t *testing.T) {
	resetMCHostHealth()
	os.Unsetenv("PRISMPANEL_MC_MIRROR") // 自动模式：官方优先
	// 版本清单：官方优先，镜像 root 兜底
	list := mcCandidateURLs("https://launchermeta.mojang.com/mc/game/version_manifest_v2.json")
	if len(list) == 0 || list[0] != "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json" {
		t.Fatalf("auto mode: official should be first: %v", list)
	}
	if !containsURL(list, "https://bmclapi2.bangbang93.com/mc/game/version_manifest_v2.json") {
		t.Fatalf("mirror root candidate missing: %v", list)
	}
	// 普通库：官方 + 镜像 maven + 镜像 libraries
	lib := mcCandidateURLs("https://libraries.minecraft.net/net/minecraft/client.jar")
	if !containsURL(lib, "https://bmclapi2.bangbang93.com/maven/net/minecraft/client.jar") {
		t.Fatalf("maven mirror missing: %v", lib)
	}
	if !containsURL(lib, "https://bmclapi2.bangbang93.com/libraries/net/minecraft/client.jar") {
		t.Fatalf("libraries mirror missing: %v", lib)
	}
}

func TestMCCandidateURLsForced(t *testing.T) {
	resetMCHostHealth()
	os.Setenv("PRISMPANEL_MC_MIRROR", "bmclapi")
	defer os.Unsetenv("PRISMPANEL_MC_MIRROR")

	// 资源（assets）：镜像 assets
	assets := mcCandidateURLs("https://resources.download.minecraft.net/ab/abcdef123")
	if len(assets) == 0 || !strings.HasPrefix(assets[0], "https://bmclapi2.bangbang93.com/") {
		t.Fatalf("assets should prefer mirror: %v", assets)
	}
	if !containsURL(assets, "https://bmclapi2.bangbang93.com/assets/ab/abcdef123") {
		t.Fatalf("assets mirror missing: %v", assets)
	}

	// fabricmc 库：镜像优先，且不包含官方源
	fabric := mcCandidateURLs("https://maven.fabricmc.net/net/fabricmc/fabric-loader.jar")
	if len(fabric) == 0 || !strings.HasPrefix(fabric[0], "https://bmclapi2.bangbang93.com/") {
		t.Fatalf("fabric lib should prefer mirror: %v", fabric)
	}
	if containsURL(fabric, "https://maven.fabricmc.net/") {
		t.Fatalf("fabric lib should skip official: %v", fabric)
	}
	if !containsURL(fabric, "https://bmclapi2.bangbang93.com/maven/net/fabricmc/fabric-loader.jar") {
		t.Fatalf("fabric maven mirror missing: %v", fabric)
	}

	// meta.fabricmc.net：镜像 fabric-meta 优先，官方兜底
	meta := mcCandidateURLs("https://meta.fabricmc.net/v2/versions/loader/1.21.4/0.16.9/profile/json")
	if len(meta) == 0 || meta[0] != "https://bmclapi2.bangbang93.com/fabric-meta/v2/versions/loader/1.21.4/0.16.9/profile/json" {
		t.Fatalf("fabric meta should prefer mirror: %v", meta)
	}
	if !containsURL(meta, "https://meta.fabricmc.net/v2/versions/loader/1.21.4/0.16.9/profile/json") {
		t.Fatalf("fabric meta should keep official fallback: %v", meta)
	}
}

func TestMCMirrorOff(t *testing.T) {
	resetMCHostHealth()
	os.Setenv("PRISMPANEL_MC_MIRROR", "off")
	defer os.Unsetenv("PRISMPANEL_MC_MIRROR")
	list := mcCandidateURLs("https://libraries.minecraft.net/a/b.jar")
	if len(list) != 1 || list[0] != "https://libraries.minecraft.net/a/b.jar" {
		t.Fatalf("off mode should keep only official: %v", list)
	}
}

func containsURL(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// TestMCOrderCandidatesFallsBackWhenAllHostsMarkedFailed 验证下载修复：
// 健康记忆把所有候选源都标记失败（官方被墙 + 镜像短暂故障的典型场景）时，
// mcOrderCandidates 必须回退到原始候选列表逐个重试，而不是返回空列表导致
// 数千个资源文件瞬间全部失败（0 字节落盘）。
func TestMCOrderCandidatesFallsBackWhenAllHostsMarkedFailed(t *testing.T) {
	defer func() {
		mcHostHealthMu.Lock()
		mcHostHealth = map[string]mcHostState{}
		mcHostHealthMu.Unlock()
	}()

	official := "https://resources.download.minecraft.net/ab/abcdef123"
	mirror := "https://bmclapi2.bangbang93.com/assets/ab/abcdef123"
	candidates := []string{official, mirror}

	// 两个主机都被标记失败（模拟官方被墙 + 镜像故障）
	mcHostMarkFailed("resources.download.minecraft.net")
	mcHostMarkFailed("bmclapi2.bangbang93.com")

	ordered := mcOrderCandidates(candidates)
	if len(ordered) == 0 {
		t.Fatalf("fix broken: all hosts failed must fall back to original candidates, got empty list")
	}
	// 回退必须保留原始候选（顺序可任意，但两条都要在）
	if !containsURL(ordered, official) || !containsURL(ordered, mirror) {
		t.Fatalf("fix broken: fallback must include original candidates, got %v", ordered)
	}
}

// TestMCOrderCandidatesFiltersMarkedHosts 验证常规过滤行为未受影响：
// 仅镜像主机被标记失败时，只保留官方候选。
func TestMCOrderCandidatesFiltersMarkedHosts(t *testing.T) {
	defer func() {
		mcHostHealthMu.Lock()
		mcHostHealth = map[string]mcHostState{}
		mcHostHealthMu.Unlock()
	}()

	official := "https://resources.download.minecraft.net/ab/abcdef123"
	mirror := "https://bmclapi2.bangbang93.com/assets/ab/abcdef123"
	candidates := []string{official, mirror}

	mcHostMarkFailed("bmclapi2.bangbang93.com")
	ordered := mcOrderCandidates(candidates)
	if len(ordered) != 1 || ordered[0] != official {
		t.Fatalf("expected only healthy official candidate, got %v", ordered)
	}
}
