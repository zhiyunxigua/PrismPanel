package game

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FetchMCFabricLoaders 拉取指定版本可用的 Fabric Loader。
func FetchMCFabricLoaders(ctx context.Context, gameVersion string) ([]MCFabricLoader, error) {
	var loaders []MCFabricLoader
	if err := getJSON(ctx, "https://meta.fabricmc.net/v2/versions/loader/"+gameVersion, &loaders); err != nil {
		return nil, err
	}
	return loaders, nil
}

type fabricProfile struct {
	ID            string          `json:"id"`
	InheritsFrom  string          `json:"inheritsFrom"`
	MainClass     string          `json:"mainClass"`
	MinecraftArgs string          `json:"minecraftArguments"`
	Libraries     []fabricLibrary `json:"libraries"`
}

type fabricLibrary struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// InstallMCFabric 下载 Fabric Loader 到本地 .minecraft，并写入版本 JSON（继承 base 版本）。
func InstallMCFabric(ctx context.Context, gameVersion, loaderVersion string, report func(stage, message string, percent float64)) (string, error) {
	installed, err := MCFabricInstalled(gameVersion)
	if err != nil {
		return "", err
	}
	if installed {
		return "", fmt.Errorf("版本 %s 已安装 Fabric Loader，不能重复安装", gameVersion)
	}
	mcDir, err := MCMinecraftDir(gameVersion)
	if err != nil {
		return "", err
	}
	endpoint := "https://meta.fabricmc.net/v2/versions/loader/" + gameVersion + "/" + loaderVersion + "/profile/json"
	if report != nil {
		report("download", "正在获取 Fabric 配置", 1)
	}
	// 只请求一次：原始字节既用于解析也原样落盘（version JSON 必须保留 arguments 等未解析字段）
	raw, err := getRawBytes(ctx, endpoint)
	if err != nil {
		return "", err
	}
	var profile fabricProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return "", fmt.Errorf("解析 Fabric 配置失败: %w", err)
	}
	versionID := profile.ID
	if versionID == "" {
		versionID = "fabric-loader-" + loaderVersion + "-" + gameVersion
	}
	versionJSONPath := filepath.Join(mcDir, "versions", versionID, versionID+".json")
	if err := os.MkdirAll(filepath.Dir(versionJSONPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(versionJSONPath, raw, 0o644); err != nil {
		return "", err
	}
	if report != nil {
		report("download", "正在下载 Fabric 依赖库", 10)
	}
	libRoot := filepath.Join(mcDir, "libraries")
	total := len(profile.Libraries)
	// 并发下载库（参照 PCL2 多线程下载；失败收集首个错误）
	var wg sync.WaitGroup
	sem := make(chan struct{}, mcEffectiveConcurrency())
	var mu sync.Mutex
	var done int
	var firstErr error
	for _, library := range profile.Libraries {
		if library.Name == "" {
			continue
		}
		url := fabricLibraryURL(library.Name, library.URL)
		if url == "" {
			continue
		}
		target := filepath.Join(libRoot, filepath.FromSlash(fabricLibraryPath(library.Name)))
		wg.Add(1)
		sem <- struct{}{}
		go func(name, url, target string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := downloadURLTo(ctx, url, target); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("download fabric library %s: %w", name, err)
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			done++
			current := done
			mu.Unlock()
			if report != nil && total > 0 {
				report("download", fmt.Sprintf("Fabric 依赖库 %d/%d", current, total), 10+float64(current)/float64(total)*85)
			}
		}(library.Name, url, target)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if firstErr != nil {
		return "", firstErr
	}
	if report != nil {
		report("install", "Fabric 安装完成", 100)
	}
	return versionID, nil
}

// fabricLibraryURL 返回 Fabric 库的下载地址：优先 profile 提供的 url；
// profile 提供的 url 可能是 maven 基地址（如 https://maven.fabricmc.net/，无 jar 路径，
// Fabric loader 0.15+ 的 profile 对部分库只给基地址）——此时按 maven 坐标推导路径补全；
// 完全无 url 时同样按坐标拼官方 maven 地址（下载时再走镜像候选，对应 PCL2 DlSourceLibraryGet 语义）。
func fabricLibraryURL(name, provided string) string {
	provided = strings.TrimSpace(provided)
	if provided != "" {
		parsed, err := url.Parse(provided)
		if err == nil && (parsed.Path == "" || parsed.Path == "/") {
			path := filepath.ToSlash(fabricLibraryPath(name))
			if path != "" && strings.Contains(path, "/") {
				return strings.TrimRight(provided, "/") + "/" + path
			}
		}
		return provided
	}
	path := filepath.ToSlash(fabricLibraryPath(name))
	if strings.Contains(path, "/") {
		return "https://maven.fabricmc.net/" + path
	}
	return ""
}

// fabricLibraryPath 由 maven 坐标推导本地库路径。
func fabricLibraryPath(name string) string {
	parts := strings.Split(name, ":")
	if len(parts) < 3 {
		return name
	}
	group := strings.ReplaceAll(parts[0], ".", "/")
	artifact := parts[1]
	version := parts[2]
	fileName := artifact + "-" + version + ".jar"
	if len(parts) >= 4 && parts[3] != "" {
		fileName = artifact + "-" + version + "-" + parts[3] + ".jar"
	}
	return filepath.Join(group, artifact, version, fileName)
}

func fabricProfileExists(mcDir, versionID string) bool {
	_, err := os.Stat(filepath.Join(mcDir, "versions", versionID, versionID+".json"))
	return err == nil
}

// MCFabricInstalled 判断指定版本是否已安装 Fabric Loader。
// 要求存在 fabric-loader-* 目录且其版本 JSON 完整（防止上次安装中途失败留下的空目录被误判为已安装）。
func MCFabricInstalled(gameVersion string) (bool, error) {
	mcDir, err := MCMinecraftDir(gameVersion)
	if err != nil {
		return false, err
	}
	versionsDir := filepath.Join(mcDir, "versions")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "fabric-loader-") {
			if _, statErr := os.Stat(filepath.Join(versionsDir, entry.Name(), entry.Name()+".json")); statErr == nil {
				return true, nil
			}
		}
	}
	return false, nil
}

// DeleteMCVersion 删除指定版本。基础版本删除整个独立目录；Fabric 子版本删除其版本 JSON 与 jar。
func DeleteMCVersion(versionID string) error {
	root, err := mcStoreRoot()
	if err != nil {
		return err
	}
	base := mcVanillaVersion(versionID)
	target := filepath.Join(root, safePathSegment(base))
	if base != versionID {
		// Fabric 子版本：仅删除 minecraft/<base>/.minecraft/versions/<versionID>
		target = filepath.Join(target, ".minecraft", "versions", safePathSegment(versionID))
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if !pathWithin(absRoot, absTarget) {
		return fmt.Errorf("拒绝删除存储根之外的目录: %s", versionID)
	}
	if _, err := os.Stat(absTarget); os.IsNotExist(err) {
		return nil
	}
	// Windows 上残留的 .part / 刚写完的文件可能被系统或安全软件短暂占用，重试几次
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if err := os.RemoveAll(absTarget); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}
	return fmt.Errorf("删除版本失败（文件可能被占用）: %w", lastErr)
}

// MCFabricVersionFor 返回指定基础版本已安装的 Fabric 子版本 id；未安装返回空串。
func MCFabricVersionFor(base string) string {
	mcDir, err := MCMinecraftDir(base)
	if err != nil {
		return ""
	}
	versionsDir := filepath.Join(mcDir, "versions")
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "fabric-loader-") {
			if _, statErr := os.Stat(filepath.Join(versionsDir, entry.Name(), entry.Name()+".json")); statErr == nil {
				return entry.Name()
			}
		}
	}
	return ""
}

func decodeVersionJSON(mcDir, versionID string) (mcVersionJSON, error) {
	path := filepath.Join(mcDir, "versions", versionID, versionID+".json")
	contents, err := os.ReadFile(path)
	if err != nil {
		return mcVersionJSON{}, err
	}
	var version mcVersionJSON
	if err := json.Unmarshal(contents, &version); err != nil {
		return mcVersionJSON{}, err
	}
	version.ArgumentGame = version.Arguments.Game
	version.ArgumentJVM = version.Arguments.JVM
	return version, nil
}
