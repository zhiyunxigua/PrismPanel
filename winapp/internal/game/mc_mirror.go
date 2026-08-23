package game

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultMCMirror = "https://bmclapi2.bangbang93.com/"

// mcConfiguredMirror 返回用户配置的镜像地址；未配置返回空。
// 优先级：环境变量 PRISMPANEL_MC_MIRROR（高级覆盖）> 全局设置（minecraft/settings.json）。
func mcConfiguredMirror() string {
	if value := strings.TrimSpace(os.Getenv("PRISMPANEL_MC_MIRROR")); value != "" {
		return normalizeMirror(value)
	}
	if settings, ok := loadCachedSettings(); ok && strings.TrimSpace(settings.Mirror) != "" {
		return normalizeMirror(settings.Mirror)
	}
	return ""
}

func normalizeMirror(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "auto") {
		return ""
	}
	if strings.EqualFold(value, "bmclapi") {
		return defaultMCMirror
	}
	if strings.HasPrefix(strings.ToLower(value), "http") {
		return strings.TrimRight(value, "/") + "/"
	}
	return ""
}

// mcEffectiveMirror 返回实际用于改写的镜像地址（未配置时用默认 BMCLAPI）。
func mcEffectiveMirror() string {
	if configured := mcConfiguredMirror(); configured != "" {
		return configured
	}
	return defaultMCMirror
}

func mcMirrorOff() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PRISMPANEL_MC_MIRROR")), "off") {
		return true
	}
	if settings, ok := loadCachedSettings(); ok && strings.EqualFold(strings.TrimSpace(settings.Mirror), "off") {
		return true
	}
	return false
}

// mcMirrorForced 是否强制使用镜像（不尝试官方源）。
func mcMirrorForced() bool {
	return mcConfiguredMirror() != ""
}

// mcRewritePath 按模式把原始 URL 改写为镜像地址（保留路径）。
// mode: "root" / "maven" / "libraries" / "assets"
func mcRewritePath(raw, mirror, mode string) string {
	if raw == "" || mirror == "" {
		return raw
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return raw
	}
	path := parsed.Path
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	switch strings.ToLower(mode) {
	case "maven":
		return mirror + "maven" + path
	case "libraries":
		return mirror + "libraries" + path
	case "assets":
		return mirror + "assets" + path
	case "fabric-meta":
		return mirror + "fabric-meta" + path
	default:
		return strings.TrimRight(mirror, "/") + path
	}
}

// mcCandidateURLs 生成下载候选源列表，按 PCL2 的源优先级（DlSourceOrder）：
//   - loader（fabricmc/minecraftforge/neoforged）库：不添加官方源，只走镜像 maven/libraries；
//   - 强制镜像模式（PRISMPANEL_MC_MIRROR=bmclapi/自定义）：镜像优先、官方兜底；
//   - 自动模式：默认镜像优先（PCL2 默认），仅当官方源测速 <4s 时才官方优先；
//   - assets：同样按上述顺序（官方源 + 镜像 assets）。
func mcCandidateURLs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	// 归一化 http 资源地址
	raw = strings.ReplaceAll(raw, "http://resources.download.minecraft.net", "https://resources.download.minecraft.net")

	isLoader := strings.Contains(raw, "fabricmc") || strings.Contains(raw, "minecraftforge") || strings.Contains(raw, "neoforged")
	isAssets := strings.Contains(raw, "resources.download.minecraft.net")
	isFabricMeta := strings.Contains(raw, "meta.fabricmc.net")

	var candidates []string
	if mcMirrorOff() {
		candidates = append(candidates, raw)
		return dedupeURLs(candidates)
	}

	mirror := mcEffectiveMirror()
	// 镜像优先的情况：强制镜像、loader 库、fabric-meta、以及自动模式下官方源测速未通过
	preferMirror := mcMirrorForced() || isLoader || isFabricMeta
	if !preferMirror && !mcSourcePrefersOfficial() {
		preferMirror = true
	}
	if !preferMirror {
		candidates = append(candidates, raw) // 官方优先
	}
	switch {
	case isFabricMeta:
		// BMCLAPI 的 Fabric meta 镜像路径
		candidates = append(candidates, mcRewritePath(raw, mirror, "fabric-meta"))
	case isAssets:
		candidates = append(candidates, mcRewritePath(raw, mirror, "assets"))
	case strings.Contains(raw, "libraries.minecraft.net"):
		// 官方库域名：BMCLAPI maven/libraries 双候选（参照 PCL2 DlSourceLibraryGet）；
		// 不生成 root 候选，避免每个库都先打一次必 404 的根路径请求。
		candidates = append(candidates,
			mcRewritePath(raw, mirror, "maven"),
			mcRewritePath(raw, mirror, "libraries"),
		)
	case isLoader:
		// fabricmc/forge/neoforge 的 maven 库：BMCLAPI maven 单候选（PCL2 语义，不生成 root/libraries）。
		candidates = append(candidates, mcRewritePath(raw, mirror, "maven"))
	default:
		// launchermeta / piston-meta / piston-data 等：镜像根路径
		// （参照 PCL2 DlSourceLauncherOrMetaGet；BMCLAPI 实测镜像根路径可命中）。
		candidates = append(candidates, mcRewritePath(raw, mirror, "root"))
	}
	if preferMirror {
		// 镜像优先时，官方源放在最后兜底；
		// 仅纯 maven 库（fabricmc/minecraftforge/neoforged 且非 meta）不添加官方源（参照 PCL2）。
		if !(isLoader && !isFabricMeta) {
			candidates = append(candidates, raw)
		}
	}
	return mcOrderCandidates(dedupeURLs(candidates))
}

func dedupeURLs(list []string) []string {
	seen := make(map[string]struct{}, len(list))
	out := make([]string, 0, len(list))
	for _, item := range list {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

// ---- 下载源健康记忆 ----
// 官方源不可达/极慢时，后续下载应尽快回退镜像，而不是每个文件都重新等一次超时。
// （大文件、数千个资源文件的场景下，逐文件重试官方源会表现为进度条卡死。）

const mcHostBlockDuration = 10 * time.Minute

// mcRateLimitBackoff 429 限流退避时长（PCL2 为 10s；这里取 5s，兼顾镜像侧限流与用户体验）。
const mcRateLimitBackoff = 5 * time.Second

var (
	mcHostHealthMu sync.Mutex
	mcHostHealth   = map[string]mcHostState{}
)

type mcHostState struct {
	failed time.Time // 网络级失败，到期前跳过该源
	slow   time.Time // 速度过慢，到期前把该源放到候选末尾
}

func mcHostMarkFailed(host string) {
	if host == "" {
		return
	}
	mcHostHealthMu.Lock()
	state := mcHostHealth[host]
	state.failed = time.Now().Add(mcHostBlockDuration)
	mcHostHealth[host] = state
	mcHostHealthMu.Unlock()
}

func mcHostMarkSlow(host string) {
	if host == "" {
		return
	}
	mcHostHealthMu.Lock()
	state := mcHostHealth[host]
	state.slow = time.Now().Add(mcHostBlockDuration)
	mcHostHealth[host] = state
	mcHostHealthMu.Unlock()
}

// mcHostHealthCheck 返回该主机当前是否被标记为失败/慢速（并清理过期状态）。
func mcHostHealthCheck(host string) (failed, slow bool) {
	if host == "" {
		return false, false
	}
	mcHostHealthMu.Lock()
	defer mcHostHealthMu.Unlock()
	state, ok := mcHostHealth[host]
	if !ok {
		return false, false
	}
	now := time.Now()
	if !state.failed.IsZero() && now.After(state.failed) {
		state.failed = time.Time{}
	}
	if !state.slow.IsZero() && now.After(state.slow) {
		state.slow = time.Time{}
	}
	if state.failed.IsZero() && state.slow.IsZero() {
		delete(mcHostHealth, host)
	} else {
		mcHostHealth[host] = state
	}
	return !state.failed.IsZero(), !state.slow.IsZero()
}

// mcOrderCandidates 过滤/排序候选下载源：失败主机跳过，慢速主机放到最后。
// 若健康记忆把全部候选源都过滤掉了（典型场景：官方源被墙 + 镜像短暂故障被标记失败），
// 回退到原始候选列表逐个重试，避免 10 分钟屏蔽期内数千个文件全部瞬间失败（0 字节落盘）。
func mcOrderCandidates(candidates []string) []string {
	healthy := make([]string, 0, len(candidates))
	slow := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Host == "" {
			healthy = append(healthy, candidate)
			continue
		}
		failed, isSlow := mcHostHealthCheck(parsed.Host)
		if failed {
			continue
		}
		if isSlow {
			slow = append(slow, candidate)
			continue
		}
		healthy = append(healthy, candidate)
	}
	result := append(healthy, slow...)
	if len(result) == 0 && len(candidates) > 0 {
		return candidates
	}
	return result
}

// ---- 官方源测速（参照 PCL2 DlPreferMojang）----
// 下载文件时默认镜像优先、官方兜底；仅当官方源响应 <4s 时才官方优先。

var (
	mcPreferOfficialMu sync.Mutex
	mcPreferOfficial   = -1 // -1 未测速 / 0 官方慢（镜像优先）/ 1 官方快（官方优先）
	mcPreferOfficialAt time.Time
)

// mcSourcePrefersOfficial 返回当前是否应优先使用官方源（惰性测速并缓存；每 10 分钟重测一次，
// 避免网络恢复后长期停留在镜像优先）。
func mcSourcePrefersOfficial() bool {
	mcPreferOfficialMu.Lock()
	defer mcPreferOfficialMu.Unlock()
	if mcPreferOfficial < 0 || time.Since(mcPreferOfficialAt) > mcHostBlockDuration {
		mcPreferOfficial = mcProbeOfficialSource()
		mcPreferOfficialAt = time.Now()
	}
	return mcPreferOfficial == 1
}

// mcProbeOfficialSource 用官方版本清单探测延迟；<4s 视为官方可用。
func mcProbeOfficialSource() int {
	request, err := http.NewRequest(http.MethodGet, "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json", nil)
	if err != nil {
		return 0
	}
	client := &http.Client{Timeout: 8 * time.Second, Transport: mcHTTPClient.Transport}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return 0
	}
	_ = response.Body.Close()
	if time.Since(started) < 4*time.Second {
		return 1
	}
	return 0
}
