package game

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const mcVersionManifestURL = "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json"

// mcDownloadUserAgent 下载请求的 User-Agent；部分镜像/反代（如 BMCLAPI 的
// maven/libraries 反代）对无 UA 或浏览器 UA 的请求返回 403。
const mcDownloadUserAgent = "PrismPanel/0.0.1"

// mcHTTPClient 带超时的 HTTP 客户端（避免镜像/网络卡死导致启动挂住）。
// 细分超时：拨号 15s、响应头 30s、整体 60s——官方源不可达/极慢时能较快回退到镜像。
var mcHTTPClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
	},
}

// mcHTTPClientLong 大文件下载用客户端：保留拨号/响应头超时防卡死，但取消整体 60s 上限，
// 避免慢速网络下 client jar（~50MB）/ Java 运行时（60-100MB）中途整体超时被丢弃重下。
// 用户取消任务（ctx）仍能立即终止。
var mcHTTPClientLong = &http.Client{
	Timeout:   30 * time.Minute,
	Transport: mcHTTPClient.Transport,
}

type mcManifest struct {
	Versions []struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		ReleaseTime string `json:"releaseTime"`
		URL         string `json:"url"`
	} `json:"versions"`
}

// mcDownloadArtifact 库文件下载描述（version JSON 提供 sha1/size 校验值）。
type mcDownloadArtifact struct {
	Path string `json:"path"`
	URL  string `json:"url"`
	SHA1 string `json:"sha1"`
	Size int64  `json:"size"`
}

type mcLibrary struct {
	Name  string `json:"name"`
	Rules []struct {
		Action string `json:"action"`
		OS     struct {
			Name string `json:"name"`
			Arch string `json:"arch"`
		} `json:"os"`
		Features *struct {
			IsDemoUser *bool `json:"is_demo_user"`
		} `json:"features"`
	} `json:"rules"`
	Downloads struct {
		Artifact    mcDownloadArtifact            `json:"artifact"`
		Classifiers map[string]mcDownloadArtifact `json:"classifiers"`
	} `json:"downloads"`
}

type mcVersionJSON struct {
	ID                 string `json:"id"`
	InheritsFrom       string `json:"inheritsFrom"`
	MainClass          string `json:"mainClass"`
	MinecraftArguments string `json:"minecraftArguments"`
	AssetIndex         struct {
		ID   string `json:"id"`
		URL  string `json:"url"`
		SHA1 string `json:"sha1"`
		Size int64  `json:"size"`
	} `json:"assetIndex"`
	Downloads struct {
		Client mcDownloadArtifact `json:"client"`
	} `json:"downloads"`
	JavaVersion struct {
		MajorVersion int    `json:"majorVersion"`
		Component    string `json:"component"`
	} `json:"javaVersion"`
	Libraries []mcLibrary `json:"libraries"`
	Arguments struct {
		Game []json.RawMessage `json:"game"`
		JVM  []json.RawMessage `json:"jvm"`
	} `json:"arguments"`
	ArgumentGame []json.RawMessage `json:"-"`
	ArgumentJVM  []json.RawMessage `json:"-"`
}

// mcStoreRoot 返回国际版启动器的存储根目录（默认在 WinApp 所在目录下的 minecraft/，
// 可用环境变量 PRISMPANEL_MC_DIR 覆盖，或在下载设置中指定“游戏目录”）。
// 每个版本一个独立 .minecraft。
func mcStoreRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv("PRISMPANEL_MC_DIR")); value != "" {
		return filepath.Abs(value)
	}
	if settings, ok := loadCachedSettings(); ok && strings.TrimSpace(settings.GameDir) != "" {
		if root, err := filepath.Abs(strings.TrimSpace(settings.GameDir)); err == nil {
			return root, nil
		}
	}
	return baseStoreRoot()
}

// InstalledMCVersions 列出本地已安装的国际版版本（含 Fabric 标记）。
func InstalledMCVersions() ([]MCInstalledVersion, error) {
	root, err := mcStoreRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var result []MCInstalledVersion
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		versionID := entry.Name()
		versionsDir := filepath.Join(root, versionID, ".minecraft", "versions")
		subs, err := os.ReadDir(versionsDir)
		if err != nil {
			continue
		}
		for _, sub := range subs {
			if !sub.IsDir() {
				continue
			}
			name := sub.Name()
			if name == "natives" {
				continue
			}
			if _, statErr := os.Stat(filepath.Join(versionsDir, name, name+".json")); statErr != nil {
				continue
			}
			fabric := strings.HasPrefix(name, "fabric-loader-")
			result = append(result, MCInstalledVersion{ID: name, Fabric: fabric, Installed: true})
		}
	}
	return result, nil
}

// MCMinecraftDir 返回指定版本的独立 .minecraft 目录。
// Fabric 版本（fabric-loader-...）与其基础版本共享同一个 .minecraft（mods 等按基础版本隔离）。
func MCMinecraftDir(versionID string) (string, error) {
	base := mcVanillaVersion(versionID)
	root, err := mcStoreRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, safePathSegment(base), ".minecraft"), nil
}

// FetchMCVersions 拉取 Mojang 版本清单。
func FetchMCVersions(ctx context.Context) ([]MCVersionEntry, error) {
	var manifest mcManifest
	if err := getJSON(ctx, mcVersionManifestURL, &manifest); err != nil {
		return nil, err
	}
	result := make([]MCVersionEntry, 0, len(manifest.Versions))
	seen := make(map[string]struct{})
	for _, version := range manifest.Versions {
		if _, ok := seen[version.ID]; ok {
			continue
		}
		seen[version.ID] = struct{}{}
		result = append(result, MCVersionEntry{ID: version.ID, Type: version.Type, ReleaseTime: version.ReleaseTime})
	}
	return result, nil
}

// InstallMCVersion 下载指定国际版版本：client jar、libraries、assets。
// versionID 可以是 Mojang 正式版本 id，也可以是自定义版本 JSON 直链（http/https）。
func InstallMCVersion(ctx context.Context, versionID string, report func(stage, message string, percent float64)) error {
	var version mcVersionJSON
	var versionJSONURL string
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(versionID)), "http") {
		versionJSONURL = strings.TrimSpace(versionID)
		if report != nil {
			report("download", "正在读取自定义版本描述", 1)
		}
		if err := getJSON(ctx, versionJSONURL, &version); err != nil {
			return err
		}
		if strings.TrimSpace(version.ID) == "" {
			return errors.New("自定义版本 JSON 缺少 id 字段")
		}
		versionID = version.ID
	} else {
		var err error
		versionJSONURL, err = resolveVersionJSONURL(ctx, versionID)
		if err != nil {
			return fmt.Errorf("%w；如为自定义/第三方版本，请粘贴版本 JSON 直链", err)
		}
		if report != nil {
			report("download", "正在下载版本描述 "+versionID, 1)
		}
		if err := getJSON(ctx, versionJSONURL, &version); err != nil {
			return err
		}
	}
	mcDir, err := MCMinecraftDir(versionID)
	if err != nil {
		return err
	}
	versionDir := filepath.Join(mcDir, "versions", versionID)
	// P2-2：versionID 来自版本 JSON（自定义直链），写盘前校验版本目录在 .minecraft 内
	if err := mcTargetWithin(mcDir, versionDir); err != nil {
		return err
	}
	versionJSONPath := filepath.Join(versionDir, versionID+".json")
	if err := downloadURLTo(ctx, versionJSONURL, versionJSONPath); err != nil {
		return err
	}
	if report != nil {
		report("download", "正在下载客户端 "+versionID+".jar", 5)
	}
	clientJar := filepath.Join(versionDir, versionID+".jar")
	if err := downloadURLWithProgress(ctx, version.Downloads.Client.URL, clientJar, "客户端 "+versionID+".jar", report, 5, 8, version.Downloads.Client.Size, version.Downloads.Client.SHA1); err != nil {
		return err
	}

	if report != nil {
		report("download", "正在下载资源索引", 8)
	}
	assetIndexID := version.AssetIndex.ID
	if assetIndexID == "" {
		assetIndexID = versionID
	}
	indexTarget := filepath.Join(mcDir, "assets", "indexes", assetIndexID+".json")
	// P2-2：assetIndex.id 来自版本 JSON，写盘前校验目标在 .minecraft 内
	if err := mcTargetWithin(mcDir, indexTarget); err != nil {
		return err
	}
	if err := downloadURLChecked(ctx, version.AssetIndex.URL, indexTarget, version.AssetIndex.Size, version.AssetIndex.SHA1); err != nil {
		return err
	}
	if err := downloadMCAssets(ctx, mcDir, indexTarget, report); err != nil {
		return err
	}

	_, err = downloadMCLibraries(ctx, mcDir, version.Libraries, report)
	return err
}

func resolveVersionJSONURL(ctx context.Context, versionID string) (string, error) {
	var manifest mcManifest
	if err := getJSON(ctx, mcVersionManifestURL, &manifest); err != nil {
		return "", err
	}
	for _, version := range manifest.Versions {
		if version.ID == versionID {
			return version.URL, nil
		}
	}
	return "", fmt.Errorf("game version %s not found in Mojang manifest", versionID)
}

func downloadMCAssets(ctx context.Context, mcDir, indexPath string, report func(string, string, float64)) error {
	contents, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}
	var index struct {
		Objects map[string]struct {
			Hash string `json:"hash"`
			Size int64  `json:"size"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(contents, &index); err != nil {
		return fmt.Errorf("decode asset index: %w", err)
	}
	total := len(index.Objects)
	if total == 0 {
		return nil
	}
	assetsRoot := filepath.Join(mcDir, "assets", "objects")
	if report != nil {
		report("download", fmt.Sprintf("正在下载 %d 个资源文件", total), 10)
	}

	// 快速失败阈值：连续失败达到该数量且零成功时提前终止，
	// 避免源不可达时 5057 个文件每个都等完整连接超时（数小时"假死"）。
	earlyAbort := 24
	if earlyAbort > total {
		earlyAbort = total
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// 并发下载 + 单个失败重试 + 部分失败不中断（个别资源缺失不影响启动，参照 PCL2 的容错）
	var mu sync.Mutex
	var downloaded, failed int
	var firstErr error
	var wg sync.WaitGroup
	sem := make(chan struct{}, mcEffectiveConcurrency())
	for _, obj := range index.Objects {
		// 注意：map key 是资源路径（如 "minecraft/sounds/.../x.ogg"），
		// 下载 URL 与磁盘路径必须用 obj.Hash（sha1），否则全部 404。
		hash := obj.Hash
		size := obj.Size
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			target := filepath.Join(assetsRoot, hash[:2], hash)
			// 已存在且尺寸/sha1 与资源索引一致才算完成，损坏/半截文件会被重新下载
			if fileMatchesExpectation(target, size, hash) {
				mu.Lock()
				downloaded++
				mu.Unlock()
				return
			}
			if err := downloadAssetWithRetry(ctx, hash, size, target); err != nil {
				mu.Lock()
				failed++
				if firstErr == nil {
					firstErr = err
				}
				abort := downloaded == 0 && failed >= earlyAbort && ctx.Err() == nil
				progress := downloaded + failed
				mu.Unlock()
				if abort {
					cancel() // 全部失败，提前终止剩余下载
				}
				if report != nil && progress%10 == 0 {
					report("assets", fmt.Sprintf("资源文件 %d/%d（失败 %d）", progress, total, failed), 10+float64(progress)/float64(total)*70)
				}
				return
			}
			mu.Lock()
			downloaded++
			progress := downloaded + failed
			mu.Unlock()
			if report != nil && progress%10 == 0 {
				report("assets", fmt.Sprintf("资源文件 %d/%d（失败 %d）", progress, total, failed), 10+float64(progress)/float64(total)*70)
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		if downloaded == 0 && firstErr != nil {
			return fmt.Errorf("资源文件下载失败（前 %d 个全部失败，已提前终止）：%v", failed, firstErr)
		}
		return ctx.Err()
	}
	if report != nil {
		report("download", fmt.Sprintf("资源文件 %d/%d", downloaded+failed, total), 80)
	}
	if failed == total {
		if firstErr != nil {
			return fmt.Errorf("资源文件全部下载失败（%d 个）：%v", failed, firstErr)
		}
		return fmt.Errorf("资源文件全部下载失败（%d 个）", failed)
	}
	if failed > 0 && report != nil {
		report("download", fmt.Sprintf("有 %d 个资源文件下载失败（不影响启动）", failed), 82)
	}
	return nil
}

// downloadAssetWithRetry 单个资源下载：按资源索引的 size/sha1 校验，尝试官方+镜像源，并整体重试一次。
// 资源文件多为小文件，单次尝试限时 10s：源不可达时快速失败换源，避免数千个文件
// 每个都等完整连接超时（15s+）导致下载长时间"假死"。
func downloadAssetWithRetry(ctx context.Context, hash string, size int64, target string) error {
	url := "https://resources.download.minecraft.net/" + hash[:2] + "/" + hash
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := downloadURLChecked(attemptCtx, url, target, size, hash)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

// downloadMCLibraries 下载所有符合规则的库；返回已下载的 jar 路径。
func downloadMCLibraries(ctx context.Context, mcDir string, libraries []mcLibrary, report func(string, string, float64)) ([]string, error) {
	libRoot := filepath.Join(mcDir, "libraries")
	paths := make([]string, 0, len(libraries))
	total := len(libraries)
	done := 0
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	sem := make(chan struct{}, mcEffectiveConcurrency())
	for _, library := range libraries {
		library := library
		if !mcLibraryAllowed(library) {
			continue
		}
		// 26.2+ 独立 natives 条目由 downloadMCNatives 统一处理（下载匹配平台变体并解压）
		if strings.Contains(library.Name, ":natives-") {
			continue
		}
		if _, hasNative := library.Downloads.Classifiers[nativeClassifier()]; hasNative {
			continue
		}
		if library.Downloads.Artifact.URL == "" || library.Downloads.Artifact.Path == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(lib mcLibrary) {
			defer wg.Done()
			defer func() { <-sem }()
			artifact := lib.Downloads.Artifact
			target := filepath.Join(libRoot, filepath.FromSlash(artifact.Path))
			// P2-2：版本 JSON 推导的下载目标必须位于 .minecraft 内，拒绝 ../ 逃逸
			if err := mcTargetWithin(mcDir, target); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if err := downloadURLChecked(ctx, artifact.URL, target, artifact.Size, artifact.SHA1); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			paths = append(paths, target)
			done++
			mu.Unlock()
			if report != nil {
				report("download", fmt.Sprintf("依赖库 %d/%d", done, total), 80+float64(done)/float64(total)*15)
			}
		}(library)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := downloadMCNatives(ctx, mcDir, libraries, report); err != nil {
		return nil, err
	}
	return paths, nil
}

func nativeClassifier() string {
	if runtime.GOOS == "windows" {
		return "natives-windows"
	}
	return "natives-" + runtime.GOOS
}

// mcNativesVariantAllowed 判断 26.2+ 版本 JSON 的"独立 natives 条目"是否属于当前平台/架构。
// 26.2 起 Mojang 把 natives 展开为独立 library 条目（name 形如
// "org.lwjgl:lwjgl-stb:3.4.1:natives-windows-arm64"），其 rules 仅按 os 匹配、
// 不区分架构——若不按本机架构过滤，会把 arm64/x86 等无关变体全部下载
// （镜像对这些文件可能 403/404，且白白占用带宽）。
func mcNativesVariantAllowed(name string) bool {
	const marker = ":natives-"
	idx := strings.LastIndex(name, marker)
	if idx < 0 {
		return true // 非 natives 条目
	}
	suffix := name[idx+len(marker):]
	switch runtime.GOOS {
	case "windows":
		switch runtime.GOARCH {
		case "arm64":
			return suffix == "windows-arm64"
		case "386":
			return suffix == "windows-x86"
		default:
			return suffix == "windows"
		}
	case "linux":
		if runtime.GOARCH == "arm64" {
			return suffix == "linux-arm64"
		}
		return suffix == "linux"
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return suffix == "macos-arm64" || suffix == "osx-arm64"
		}
		return suffix == "macos" || suffix == "osx"
	}
	return false
}

func downloadMCNatives(ctx context.Context, mcDir string, libraries []mcLibrary, report func(string, string, float64)) error {
	versionNatives := filepath.Join(mcDir, "versions", "natives")
	if err := os.MkdirAll(versionNatives, 0o755); err != nil {
		return err
	}
	for _, library := range libraries {
		if !mcLibraryAllowed(library) {
			continue
		}
		// 26.2+ 独立 natives 条目：按平台/架构过滤后下载并解压
		if strings.Contains(library.Name, ":natives-") {
			if !mcNativesVariantAllowed(library.Name) {
				continue
			}
			artifact := library.Downloads.Artifact
			if artifact.URL == "" || artifact.Path == "" {
				continue
			}
			archive := filepath.Join(mcDir, "libraries", filepath.FromSlash(artifact.Path))
			// P2-2：natives 压缩包目标同样必须位于 .minecraft 内
			if err := mcTargetWithin(mcDir, archive); err != nil {
				return err
			}
			if err := downloadURLChecked(ctx, artifact.URL, archive, artifact.Size, artifact.SHA1); err != nil {
				return err
			}
			if err := extractZipFiltered(archive, versionNatives); err != nil {
				return fmt.Errorf("extract natives %s: %w", artifact.Path, err)
			}
			if report != nil {
				report("extract", "正在解压原生库", 97)
			}
			continue
		}
		classifier := library.Downloads.Classifiers[nativeClassifier()]
		if classifier.URL == "" || classifier.Path == "" {
			continue
		}
		archive := filepath.Join(mcDir, "libraries", filepath.FromSlash(classifier.Path))
		// P2-2：classifier 压缩包目标同样必须位于 .minecraft 内
		if err := mcTargetWithin(mcDir, archive); err != nil {
			return err
		}
		if err := downloadURLChecked(ctx, classifier.URL, archive, classifier.Size, classifier.SHA1); err != nil {
			return err
		}
		if err := extractZipFiltered(archive, versionNatives); err != nil {
			return fmt.Errorf("extract natives %s: %w", classifier.Path, err)
		}
		if report != nil {
			report("extract", "正在解压原生库", 97)
		}
	}
	return nil
}

func mcLibraryAllowed(library mcLibrary) bool {
	if len(library.Rules) == 0 {
		return true
	}
	allowed := false
	for _, rule := range library.Rules {
		matches := rule.OS.Name == "" || rule.OS.Name == runtime.GOOS
		if rule.OS.Arch != "" {
			matches = matches && rule.OS.Arch == runtime.GOARCH
		}
		// features.is_demo_user：启动器永不启动试玩账号 → 要求 true 的规则不匹配
		// （与 mcRulesAllow 保持一致，防止 demo 专用库/参数被误加入）。
		if rule.Features != nil && rule.Features.IsDemoUser != nil && *rule.Features.IsDemoUser {
			matches = false
		}
		if !matches {
			continue
		}
		allowed = rule.Action == "allow"
	}
	return allowed
}

// extractZipFiltered 只提取可执行原生文件（dll/so/dylib）到目标目录。
func extractZipFiltered(source, target string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, file := range reader.File {
		name := file.Name
		if !isNativeLibraryFile(name) {
			continue
		}
		clean, err := cleanArchivePath(name)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, filepath.Base(filepath.FromSlash(clean)))
		input, err := file.Open()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			input.Close()
			return err
		}
		output, err := os.Create(destination)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		_ = input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func isNativeLibraryFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".so") || strings.HasSuffix(lower, ".dylib")
}

// getRawBytes 按候选源顺序请求并返回响应体原始字节（配合 429 退避/健康记忆/镜像节流）。
func getRawBytes(ctx context.Context, endpoint string) ([]byte, error) {
	var lastErr error
	for _, candidate := range mcCandidateURLs(endpoint) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("Accept", "application/json")
		response, err := mcHTTPClient.Do(request)
		if err != nil {
			lastErr = fmt.Errorf("request %s failed: %w", candidate, err)
			if ctx.Err() == nil {
				mcHostMarkFailed(request.URL.Host)
			}
			continue
		}
		status := response.StatusCode
		if status < 200 || status >= 300 {
			response.Body.Close()
			if status == http.StatusTooManyRequests {
				// 429 限流：退避后换源（PCL2 Sleep 10s），并把该源降速
				mcHostMarkSlow(request.URL.Host)
				mcMirrorThrottle(request.URL.Host)
				time.Sleep(mcRateLimitBackoff)
				lastErr = fmt.Errorf("request %s failed: %s", candidate, response.Status)
				continue
			}
			if status >= 500 && ctx.Err() == nil {
				mcHostMarkFailed(request.URL.Host)
			}
			lastErr = fmt.Errorf("request %s failed: %s", candidate, response.Status)
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read %s: %w", candidate, readErr)
			continue
		}
		mcMirrorThrottle(request.URL.Host)
		return body, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no download source available")
	}
	return nil, lastErr
}

func getJSON(ctx context.Context, endpoint string, out any) error {
	data, err := getRawBytes(ctx, endpoint)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}

// downloadURLTo 下载到目标文件（无校验值；文件存在即跳过）。
func downloadURLTo(ctx context.Context, endpoint, destination string) error {
	return downloadURLChecked(ctx, endpoint, destination, 0, "")
}

// downloadURLChecked 下载到目标文件，可按期望 size/sha1 校验：
//   - 目标已存在且与期望值一致 → 直接跳过；
//   - 目标存在但损坏/尺寸不符 → 重新下载覆盖；
//   - 校验失败的文件不会被保留（删 .part），换下一候选源。
//
// wantSize/wantSHA1 为 0/空 时退化为"文件存在即跳过"的旧行为。
func downloadURLChecked(ctx context.Context, endpoint, destination string, wantSize int64, wantSHA1 string) error {
	if endpoint == "" || destination == "" {
		return errors.New("download url or destination is empty")
	}
	if fileMatchesExpectation(destination, wantSize, wantSHA1) {
		return nil
	}
	var lastErr error
	for _, candidate := range mcCandidateURLs(endpoint) {
		if err := downloadURLOnceChecked(ctx, candidate, destination, wantSize, wantSHA1); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no download source available")
	}
	return lastErr
}

// fileMatchesExpectation 判断目标文件是否符合期望（无期望值时仅要求存在）。
func fileMatchesExpectation(path string, wantSize int64, wantSHA1 string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if wantSize == 0 && wantSHA1 == "" {
		return true
	}
	if wantSize > 0 && info.Size() != wantSize {
		return false
	}
	if wantSHA1 == "" {
		return true
	}
	return strings.EqualFold(sha1HexOfFile(path), strings.ToLower(strings.TrimSpace(wantSHA1)))
}

// sha1HexOfFile 计算文件 sha1（失败返回空串）。
func sha1HexOfFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha1.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// reuseCompletePart 若存在完整下载好的 .part（尺寸且可选 sha1 均匹配），直接改名为最终文件，避免重新下载。
func reuseCompletePart(tmp, destination string, expectedSize int64, wantSHA1 string) bool {
	if expectedSize <= 0 {
		return false
	}
	info, err := os.Stat(tmp)
	if err != nil || info.IsDir() || info.Size() != expectedSize {
		return false
	}
	if wantSHA1 != "" && !strings.EqualFold(sha1HexOfFile(tmp), strings.ToLower(strings.TrimSpace(wantSHA1))) {
		return false
	}
	return renameOverwrite(tmp, destination) == nil
}

// renameOverwrite 重命名，Windows 上目标存在时先删除再重命名（os.Rename 不覆盖已存在目标）。
func renameOverwrite(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	// 目标被占用/存在（Windows）：先移除再重试一次
	if removeErr := os.Remove(destination); removeErr != nil && !os.IsNotExist(removeErr) {
		return fmt.Errorf("rename %s: remove target %s: %w", source, destination, removeErr)
	}
	return os.Rename(source, destination)
}

// mcMirrorThrottle BMCLAPI 请求节流（PCL2 每次请求后 Sleep(100ms) 降频，避免触发限流）。
func mcMirrorThrottle(host string) {
	if strings.Contains(strings.ToLower(host), "bmclapi") {
		time.Sleep(100 * time.Millisecond)
	}
}

// downloadURLOnce 下载单个候选源到目标文件（无校验）。
func downloadURLOnce(ctx context.Context, endpoint, destination string) error {
	return downloadURLOnceChecked(ctx, endpoint, destination, 0, "")
}

// downloadURLOnceChecked 下载单个候选源；按 wantSize/wantSHA1 校验内容，
// 校验失败删除 .part 返回错误（上层换下一候选源）。大文件走长超时客户端。
func downloadURLOnceChecked(ctx context.Context, endpoint, destination string, wantSize int64, wantSHA1 string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", mcDownloadUserAgent)
	response, err := mcHTTPClientLong.Do(request)
	if err != nil {
		if ctx.Err() == nil {
			mcHostMarkFailed(request.URL.Host)
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusTooManyRequests {
			// 429 限流：退避后换源（PCL2 Sleep 10s），并把该源降速
			mcHostMarkSlow(request.URL.Host)
			mcMirrorThrottle(request.URL.Host)
			time.Sleep(mcRateLimitBackoff)
			return fmt.Errorf("download %s failed: %s", endpoint, response.Status)
		}
		if response.StatusCode >= 500 && ctx.Err() == nil {
			mcHostMarkFailed(request.URL.Host)
		}
		return fmt.Errorf("download %s failed: %s", endpoint, response.Status)
	}
	tmp := destination + ".part"
	if reuseCompletePart(tmp, destination, response.ContentLength, wantSHA1) {
		return nil
	}
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	started := time.Now()
	var hasher hash.Hash
	if wantSHA1 != "" {
		hasher = sha1.New()
	}
	var copied int64
	if hasher != nil {
		copied, err = io.Copy(io.MultiWriter(file, hasher), response.Body)
	} else {
		copied, err = io.Copy(file, response.Body)
	}
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(tmp)
		if ctx.Err() == nil {
			mcHostMarkFailed(request.URL.Host)
		}
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if wantSize > 0 && copied != wantSize {
		_ = os.Remove(tmp)
		return fmt.Errorf("download %s size mismatch: got %d want %d", endpoint, copied, wantSize)
	}
	if hasher != nil {
		actual := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(actual, strings.ToLower(strings.TrimSpace(wantSHA1))) {
			_ = os.Remove(tmp)
			return fmt.Errorf("download %s sha1 mismatch", endpoint)
		}
	}
	// 下载成功但明显过慢（如小文件也要好几秒），之后优先走更快的镜像源
	if time.Since(started) > 3*time.Second {
		mcHostMarkSlow(request.URL.Host)
	}
	mcMirrorThrottle(request.URL.Host)
	if err := renameOverwrite(tmp, destination); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// downloadURLWithProgress 下载并实时上报进度（percentStart→percentEnd 区间），避免大文件下载时进度条卡住。
// 可按 wantSize/wantSHA1 校验内容（如 client jar）。
func downloadURLWithProgress(ctx context.Context, endpoint, destination, label string, report func(stage, message string, percent float64), percentStart, percentEnd float64, wantSize int64, wantSHA1 string) error {
	if endpoint == "" || destination == "" {
		return errors.New("download url or destination is empty")
	}
	if fileMatchesExpectation(destination, wantSize, wantSHA1) {
		return nil
	}
	var lastErr error
	for _, candidate := range mcCandidateURLs(endpoint) {
		err := downloadURLOnceProgressChecked(ctx, candidate, destination, wantSize, wantSHA1, func(done, total int64) {
			if report == nil {
				return
			}
			if total > 0 {
				percent := percentStart + float64(done)/float64(total)*(percentEnd-percentStart)
				if percent > percentEnd {
					percent = percentEnd
				}
				report("download", fmt.Sprintf("%s (%.1f/%.1f MB)", label, float64(done)/1048576, float64(total)/1048576), percent)
			} else {
				// 服务器未返回 Content-Length 时，至少更新已下载字节，避免界面看起来卡死
				report("download", fmt.Sprintf("%s (已下载 %.1f MB)", label, float64(done)/1048576), percentStart)
			}
		})
		if err == nil {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no download source available")
	}
	return lastErr
}

func downloadURLOnceProgress(ctx context.Context, endpoint, destination string, progress func(done, total int64)) error {
	return downloadURLOnceProgressChecked(ctx, endpoint, destination, 0, "", progress)
}

// downloadURLOnceProgressChecked 带校验/429 退避/节流的单源进度下载（大文件走长超时客户端）。
func downloadURLOnceProgressChecked(ctx context.Context, endpoint, destination string, wantSize int64, wantSHA1 string, progress func(done, total int64)) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", mcDownloadUserAgent)
	response, err := mcHTTPClientLong.Do(request)
	if err != nil {
		if ctx.Err() == nil {
			mcHostMarkFailed(request.URL.Host)
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusTooManyRequests {
			mcHostMarkSlow(request.URL.Host)
			mcMirrorThrottle(request.URL.Host)
			time.Sleep(mcRateLimitBackoff)
			return fmt.Errorf("download %s failed: %s", endpoint, response.Status)
		}
		if response.StatusCode >= 500 && ctx.Err() == nil {
			mcHostMarkFailed(request.URL.Host)
		}
		return fmt.Errorf("download %s failed: %s", endpoint, response.Status)
	}
	tmp := destination + ".part"
	if reuseCompletePart(tmp, destination, response.ContentLength, wantSHA1) {
		return nil
	}
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	total := response.ContentLength
	var written int64
	buffer := make([]byte, 256*1024)
	started := time.Now()
	var hasher hash.Hash
	if wantSHA1 != "" {
		hasher = sha1.New()
	}
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if hasher != nil {
				_, _ = hasher.Write(buffer[:n])
			}
			if _, writeErr := file.Write(buffer[:n]); writeErr != nil {
				_ = file.Close()
				_ = os.Remove(tmp)
				if ctx.Err() == nil {
					mcHostMarkFailed(request.URL.Host)
				}
				return writeErr
			}
			written += int64(n)
			if progress != nil {
				progress(written, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = file.Close()
			_ = os.Remove(tmp)
			if ctx.Err() == nil {
				mcHostMarkFailed(request.URL.Host)
			}
			return readErr
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if wantSize > 0 && written != wantSize {
		_ = os.Remove(tmp)
		return fmt.Errorf("download %s size mismatch: got %d want %d", endpoint, written, wantSize)
	}
	if hasher != nil {
		actual := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(actual, strings.ToLower(strings.TrimSpace(wantSHA1))) {
			_ = os.Remove(tmp)
			return fmt.Errorf("download %s sha1 mismatch", endpoint)
		}
	}
	if time.Since(started) > 3*time.Second {
		mcHostMarkSlow(request.URL.Host)
	}
	mcMirrorThrottle(request.URL.Host)
	if err := renameOverwrite(tmp, destination); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
