package game

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// javaRuntimeManifestURL Mojang Java 运行时清单（PCL2 同款固定 revision）。
const javaRuntimeManifestURL = "https://piston-meta.mojang.com/v1/products/java-runtime/2ec0cc96c44e5a76b9c8b7c39df7210883d12871/all.json"

type javaRuntimeAll struct {
	WindowsX64 map[string][]javaRuntimeRelease `json:"windows-x64"`
	Linux      map[string][]javaRuntimeRelease `json:"linux"`
}

type javaRuntimeRelease struct {
	Manifest struct {
		URL string `json:"url"`
	} `json:"manifest"`
}

type javaRuntimeManifest struct {
	Files map[string]struct {
		Downloads struct {
			Raw struct {
				URL  string `json:"url"`
				Size int64  `json:"size"`
				SHA1 string `json:"sha1"`
			} `json:"raw"`
		} `json:"downloads"`
	} `json:"files"`
}

// javaRuntimeOSKey 返回当前平台的 Java 运行时 OS 键。
func javaRuntimeOSKey() string {
	if runtime.GOOS == "windows" {
		return "windows-x64"
	}
	return "linux"
}

// mcRequiredJavaInfo 从版本 JSON 读取权威的 Java 要求（majorVersion + 运行时组件），
// 支持 Fabric 继承 base。版本未安装/无 javaVersion 时返回 0, ""。
func mcRequiredJavaInfo(versionID string) (int, string) {
	mcDir, err := MCMinecraftDir(versionID)
	if err != nil {
		return 0, ""
	}
	version, err := decodeVersionJSON(mcDir, versionID)
	if err != nil {
		return 0, ""
	}
	if inherits := strings.TrimSpace(version.InheritsFrom); inherits != "" && inherits != versionID {
		if base, err := decodeVersionJSON(mcDir, inherits); err == nil {
			version = base
		}
	}
	return version.JavaVersion.MajorVersion, strings.TrimSpace(version.JavaVersion.Component)
}

// mcJavaComponent 返回该版本所需的 Mojang Java 运行时组件名。
// 优先使用版本 JSON 的 javaVersion.component（权威），否则按大版本映射。
func mcJavaComponent(versionID string) string {
	if _, component := mcRequiredJavaInfo(versionID); component != "" {
		return component
	}
	return mcComponentForMajor(mcJavaRequirement(versionID))
}

// mcComponentForMajor 大版本 → Mojang 运行时组件（实测组件 release 文件：jre-legacy=1.8、
// alpha=16.0.1、beta/gamma=17.0.15、delta=21.0.7、epsilon=25.0.1；版本 JSON 配对：
// 1.18.2→beta、1.19.2/1.20.4→gamma、1.20.6/1.21.x→delta）。
// 22+ 未知新版本统一落到 epsilon（Mojang 当前最新运行时；未来有新组件可再细分）。
func mcComponentForMajor(required string) string {
	switch required {
	case "8":
		return "jre-legacy"
	case "16":
		return "java-runtime-alpha"
	case "17":
		return "java-runtime-gamma"
	case "21":
		return "java-runtime-delta"
	default: // 22+（如未来 majorVersion 24/25 且版本 JSON 缺 component 字段时）
		return "java-runtime-epsilon"
	}
}

func mcJavaRoot() (string, error) {
	root, err := mcStoreRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "java"), nil
}

func mcJavaComponentDir(versionID string) (string, error) {
	root, err := mcJavaRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, mcJavaComponent(versionID)), nil
}

// findJavaInStore 在本地 Java 缓存中查找指定版本所需 Java。
func findJavaInStore(versionID string) string {
	dir, err := mcJavaComponentDir(versionID)
	if err != nil {
		return ""
	}
	candidate := filepath.Join(dir, "bin", javaExeName())
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return ""
}

// javaPackedSkipHashes PCL2 跳过的 3 个巨型重复文件 sha1。
var javaPackedSkipHashes = map[string]struct{}{
	"12976a6c2b227cbac58969c1455444596c894656": {},
	"c80e4bab46e34d02826eab226a4441d0970f2aba": {},
	"84d2102ad171863db04e7ee22a259d1f6c5de4a5": {},
}

// EnsureMCJava 确保本地存在该版本所需的 Java 运行时；缺失时自动从镜像下载。
func EnsureMCJava(ctx context.Context, versionID string, report func(stage, message string, percent float64)) error {
	if findJavaInStore(versionID) != "" {
		return nil
	}
	component := mcJavaComponent(versionID)
	javaVersion := mcJavaRequirement(versionID)
	dir, err := mcJavaComponentDir(versionID)
	if err != nil {
		return err
	}
	if report != nil {
		report("java", fmt.Sprintf("正在下载 Java %s（%s）", javaVersion, component), 1)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// 1. 获取该组件的运行时清单地址
	var all javaRuntimeAll
	if err := getJSON(ctx, javaRuntimeManifestURL, &all); err != nil {
		return fmt.Errorf("获取 Java 运行时清单失败: %w", err)
	}
	releases := all.WindowsX64[component]
	if len(releases) == 0 {
		releases = all.Linux[component]
	}
	if len(releases) == 0 {
		return fmt.Errorf("未找到 Java 运行时组件 %s", component)
	}
	manifestURL := releases[0].Manifest.URL
	if manifestURL == "" {
		return fmt.Errorf("Java 运行时组件 %s 缺少清单地址", component)
	}

	// 2. 获取清单文件列表
	var manifest javaRuntimeManifest
	if err := getJSON(ctx, manifestURL, &manifest); err != nil {
		return fmt.Errorf("获取 Java 清单失败: %w", err)
	}
	total := len(manifest.Files)
	// 3. 并发下载（Java 运行时 200+ 文件 / 60-100MB，串行在国内网络很慢；参照 PCL2 多线程下载）
	var wg sync.WaitGroup
	sem := make(chan struct{}, mcEffectiveConcurrency())
	var mu sync.Mutex
	var done int
	var lastErr error
	for path, file := range manifest.Files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		raw := file.Downloads.Raw
		if raw.URL == "" {
			done++
			continue
		}
		if _, skip := javaPackedSkipHashes[strings.ToLower(strings.TrimSpace(raw.SHA1))]; skip {
			done++
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(path))
		wg.Add(1)
		sem <- struct{}{}
		go func(rawURL, target string, size int64, sha1Hex string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := downloadJavaFile(ctx, rawURL, target, size, sha1Hex); err != nil {
				mu.Lock()
				if lastErr == nil {
					lastErr = err
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			done++
			current := done
			mu.Unlock()
			if report != nil && total > 0 && current%20 == 0 {
				report("java", fmt.Sprintf("Java 文件 %d/%d", current, total), 1+float64(current)/float64(total)*97)
			}
		}(raw.URL, target, raw.Size, raw.SHA1)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if lastErr != nil {
		return lastErr
	}
	candidate := findJavaInStore(versionID)
	if candidate == "" {
		return fmt.Errorf("Java 运行时文件不完整：%s 未生成 %s", dir, filepath.Join("bin", javaExeName()))
	}
	// 下载后校验大版本（防止组件映射错误导致下载到不匹配的 Java）
	if required := mcJavaRequirement(versionID); required != "" {
		if version, err := detectJavaVersion(candidate); err == nil && version < javaMajorNumber(required) {
			return fmt.Errorf("下载的 Java 运行时版本过低（%d < 需要 %s），建议手动安装 JDK 或重新下载", version, required)
		}
	}
	if report != nil {
		report("java", "Java 运行时就绪", 100)
	}
	return nil
}

// downloadJavaFile 下载 Java 运行时文件：走候选源（官方 + 镜像根路径，参照 PCL2 L558 双源），
// 按 size + sha1 校验，失败换下一候选源。
func downloadJavaFile(ctx context.Context, url, destination string, size int64, sha1Hex string) error {
	if fileJavaMatches(destination, size, sha1Hex) {
		return nil
	}
	var lastErr error
	for _, candidate := range mcCandidateURLs(url) {
		if err := downloadJavaFileOnce(ctx, candidate, destination, size, sha1Hex); err == nil {
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

func downloadJavaFileOnce(ctx context.Context, url, destination string, size int64, sha1Hex string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// 大文件走长超时客户端（避免 60s 整体超时把 Java 运行时文件中途丢弃）
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
			return fmt.Errorf("下载 %s 失败: %s", url, response.Status)
		}
		if response.StatusCode >= 500 && ctx.Err() == nil {
			mcHostMarkFailed(request.URL.Host)
		}
		return fmt.Errorf("下载 %s 失败: %s", url, response.Status)
	}
	tmp := destination + ".part"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	hash := sha1.New()
	started := time.Now()
	_, copyErr := io.Copy(io.MultiWriter(file, hash), response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		if ctx.Err() == nil {
			mcHostMarkFailed(request.URL.Host)
		}
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if sha1Hex != "" && !strings.EqualFold(actual, strings.TrimSpace(sha1Hex)) {
		_ = os.Remove(tmp)
		return fmt.Errorf("Java 文件 %s sha1 校验失败", filepath.Base(destination))
	}
	if time.Since(started) > 3*time.Second {
		mcHostMarkSlow(request.URL.Host)
	}
	mcMirrorThrottle(request.URL.Host)
	// 目标已存在（重新下载损坏文件）时先删后改名
	return renameOverwrite(tmp, destination)
}

func fileJavaMatches(path string, size int64, sha1Hex string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if size > 0 && info.Size() != size {
		return false
	}
	if sha1Hex == "" {
		return true
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha1.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), strings.TrimSpace(sha1Hex))
}
