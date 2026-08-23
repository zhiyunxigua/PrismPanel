package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const modrinthAPIBase = "https://api.modrinth.com/v2"

// MCModEntry 已安装的 mod 信息。
type MCModEntry struct {
	Filename string    `json:"filename"` // 磁盘文件名（含扩展名）
	Enabled  bool      `json:"enabled"`  // 是否启用（禁用时以 .disabled 后缀存储）
	Size     int64     `json:"size"`     // 字节大小
	Modified time.Time `json:"modified"` // 修改时间
}

// mcModsDir 返回指定版本（或 Fabric 子版本）的 mods 目录。
func mcModsDir(versionID string) (string, error) {
	mcDir, err := MCMinecraftDir(versionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(mcDir, "mods"), nil
}

// MCModsList 列出指定版本已安装的 mod。
func MCModsList(versionID string) ([]MCModEntry, error) {
	dir, err := mcModsDir(versionID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var result []MCModEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		enabled := true
		displayName := name
		if strings.HasSuffix(strings.ToLower(name), ".disabled") {
			enabled = false
			displayName = name[:len(name)-len(".disabled")]
		}
		info, err := entry.Info()
		if err != nil {
			info = nil
		}
		entry := MCModEntry{Filename: displayName, Enabled: enabled}
		if info != nil {
			entry.Size = info.Size()
			entry.Modified = info.ModTime()
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Filename) < strings.ToLower(result[j].Filename)
	})
	return result, nil
}

// MCModsToggle 启用/禁用指定 mod（禁用时把文件重命名为 .disabled 后缀，启用时还原）。
func MCModsToggle(versionID, filename string, enabled bool) error {
	dir, err := mcModsDir(versionID)
	if err != nil {
		return err
	}
	if !mcSafeFilename(filename) {
		return errors.New("非法的 mod 文件名")
	}
	active := filepath.Join(dir, filename)
	disabled := filepath.Join(dir, filename+".disabled")
	if enabled {
		if _, err := os.Stat(disabled); os.IsNotExist(err) {
			return nil
		}
		return os.Rename(disabled, active)
	}
	if _, err := os.Stat(active); os.IsNotExist(err) {
		return nil
	}
	return os.Rename(active, disabled)
}

// MCModsDelete 删除指定 mod（含 .disabled 文件）。
func MCModsDelete(versionID, filename string) error {
	dir, err := mcModsDir(versionID)
	if err != nil {
		return err
	}
	if !mcSafeFilename(filename) {
		return errors.New("非法的 mod 文件名")
	}
	active := filepath.Join(dir, filename)
	disabled := filepath.Join(dir, filename+".disabled")
	removed := false
	for _, path := range []string{active, disabled} {
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				return err
			}
			removed = true
		}
	}
	if !removed {
		return errors.New("mod 不存在")
	}
	return nil
}

// MCModsOpenDir 打开指定版本的 mods 目录（资源管理器 / 文件管理器）。
func MCModsOpenDir(versionID string) error {
	dir, err := mcModsDir(versionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return exec.Command("explorer.exe", dir).Start()
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("open", dir).Start()
	}
	return exec.Command("xdg-open", dir).Start()
}

func mcSafeFilename(name string) bool {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "/\\:") || strings.HasPrefix(name, ".") {
		return false
	}
	return true
}

// MCModrinthHit Modrinth 搜索结果条目。
type MCModrinthHit struct {
	ProjectID     string   `json:"project_id"`
	Slug          string   `json:"slug"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Author        string   `json:"author"`
	Downloads     int64    `json:"downloads"`
	IconURL       string   `json:"icon_url"`
	LatestVersion string   `json:"latest_version"`
	Categories    []string `json:"categories"`
}

// mcModrinthCandidates 生成 Modrinth 请求候选地址（官方 + mod.mcimirror.top 镜像，参照 PCL2）。
func mcModrinthCandidates(endpoint string) []string {
	mirror := strings.Replace(endpoint, "https://api.modrinth.com", "https://mod.mcimirror.top/modrinth", 1)
	if mirror == endpoint {
		mirror = strings.Replace(endpoint, "https://cdn.modrinth.com", "https://mod.mcimirror.top", 1)
	}
	return mcOrderCandidates(dedupeURLs([]string{endpoint, mirror}))
}

// MCSearchModrinth 在 Modrinth 搜索指定游戏版本可用（且可选 loader）的 mod。
func MCSearchModrinth(ctx context.Context, query, gameVersion, loader string) ([]MCModrinthHit, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("limit", "24")
	facets := [][]string{{"versions:" + gameVersion}}
	if loader != "" {
		facets = append(facets, []string{"categories:" + loader})
	}
	encoded, _ := json.Marshal(facets)
	params.Set("facets", string(encoded))
	endpoint := modrinthAPIBase + "/search?" + params.Encode()
	var lastErr error
	for _, candidate := range mcModrinthCandidates(endpoint) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("User-Agent", "PrismPanel/1.0")
		request.Header.Set("Accept", "application/json")
		response, err := mcHTTPClient.Do(request)
		if err != nil {
			lastErr = err
			mcHostMarkFailed(request.URL.Host)
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			if response.StatusCode >= 500 {
				mcHostMarkFailed(request.URL.Host)
			}
			lastErr = fmt.Errorf("Modrinth 搜索失败：%s", response.Status)
			continue
		}
		var out struct {
			Hits []MCModrinthHit `json:"hits"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&out)
		response.Body.Close()
		if decodeErr != nil {
			lastErr = decodeErr
			continue
		}
		return out.Hits, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no download source available")
	}
	return nil, lastErr
}

// MCModrinthDependency Modrinth 版本依赖声明（dependencies 字段）。
type MCModrinthDependency struct {
	ProjectID      string `json:"project_id"`
	VersionID      string `json:"version_id"`
	DependencyType string `json:"dependency_type"` // required / optional / incompatible / embedded
}

// MCModrinthVersion Modrinth 项目的一个下载版本。
type MCModrinthVersion struct {
	ID            string   `json:"id"`
	VersionNumber string   `json:"version_number"`
	DatePublished string   `json:"date_published"`
	GameVersions  []string `json:"game_versions"`
	Loaders       []string `json:"loaders"`
	Files         []struct {
		Filename string `json:"filename"`
		URL      string `json:"url"`
		Size     int64  `json:"size"`
	} `json:"files"`
	Dependencies []MCModrinthDependency `json:"dependencies"`
}

// mcModrinthMaxDependencyDepth 依赖递归深度上限（保守实现，防环 + 防过深依赖链）。
const mcModrinthMaxDependencyDepth = 5

// mcModrinthDependencyItem 一条已解析的依赖安装项（依赖在前、本体在后）。
type mcModrinthDependencyItem struct {
	ProjectID string
	Version   *MCModrinthVersion
}

// mcModrinthVersionFetcher 获取项目可安装版本（versionID 为空时按 gameVersion/loader 挑选）的回调，
// 测试可注入假数据（fake dependency data）。
type mcModrinthVersionFetcher func(ctx context.Context, projectID, versionID, gameVersion, loader string) (*MCModrinthVersion, error)

// mcModrinthResolveDependencies 递归展开 required 依赖链（#14）：
//   - 返回顺序为"依赖在前、本体在后"，安装时先装依赖再装本体；
//   - visited 集合去重/防环：同一项目在一次安装中只解析并安装一次；
//   - 深度上限 mcModrinthMaxDependencyDepth，超限给出明确错误；
//   - dependency_type 非 required（optional/incompatible/embedded）跳过，不安装。
//
// preferredVersionID 为依赖声明的 version_id（作者固定版本）；非空时优先用该版本。
func mcModrinthResolveDependencies(ctx context.Context, fetch mcModrinthVersionFetcher, projectID, preferredVersionID, gameVersion, loader string, visited map[string]bool, depth int) ([]mcModrinthDependencyItem, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("依赖项目 id 为空")
	}
	if depth > mcModrinthMaxDependencyDepth {
		return nil, fmt.Errorf("依赖链超过 %d 层（%s 的依赖过深或成环），请手动安装", mcModrinthMaxDependencyDepth, projectID)
	}
	if visited[projectID] {
		return nil, nil // 已解析过（防环/去重）
	}
	visited[projectID] = true
	version, err := fetch(ctx, projectID, strings.TrimSpace(preferredVersionID), gameVersion, loader)
	if err != nil {
		return nil, err
	}
	if version == nil {
		return nil, fmt.Errorf("项目 %s 无可安装版本", projectID)
	}
	var result []mcModrinthDependencyItem
	for _, dep := range version.Dependencies {
		if dep.DependencyType != "required" {
			continue // optional/incompatible/embedded 不自动安装
		}
		depID := strings.TrimSpace(dep.ProjectID)
		if depID == "" {
			continue
		}
		sub, err := mcModrinthResolveDependencies(ctx, fetch, depID, dep.VersionID, gameVersion, loader, visited, depth+1)
		if err != nil {
			return nil, fmt.Errorf("依赖 %s 解析失败：%w", depID, err)
		}
		result = append(result, sub...)
	}
	result = append(result, mcModrinthDependencyItem{ProjectID: projectID, Version: version})
	return result, nil
}

// mcModrinthFetchVersion 真实 fetcher：versionID 非空时直接取该版本，否则查询项目版本列表并按兼容性挑选。
func mcModrinthFetchVersion(ctx context.Context, projectID, versionID, gameVersion, loader string) (*MCModrinthVersion, error) {
	if strings.TrimSpace(versionID) != "" {
		return mcModrinthGetVersion(ctx, strings.TrimSpace(versionID))
	}
	versions, err := mcModrinthProjectVersions(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return mcModrinthPickVersion(versions, gameVersion, loader)
}

// mcModrinthProjectVersions 查询项目版本列表（官方 + 镜像候选源）。
func mcModrinthProjectVersions(ctx context.Context, projectID string) ([]MCModrinthVersion, error) {
	endpoint := fmt.Sprintf("%s/project/%s/version", modrinthAPIBase, url.PathEscape(projectID))
	var versions []MCModrinthVersion
	var lastErr error
	fetched := false
	for _, candidate := range mcModrinthCandidates(endpoint) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("User-Agent", "PrismPanel/1.0")
		request.Header.Set("Accept", "application/json")
		response, err := mcHTTPClient.Do(request)
		if err != nil {
			lastErr = err
			if ctx.Err() == nil {
				mcHostMarkFailed(request.URL.Host)
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			if response.StatusCode >= 500 && ctx.Err() == nil {
				mcHostMarkFailed(request.URL.Host)
			}
			lastErr = fmt.Errorf("Modrinth 版本查询失败：%s", response.Status)
			continue
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&versions)
		response.Body.Close()
		if decodeErr != nil {
			lastErr = decodeErr
			continue
		}
		fetched = true
		break
	}
	if !fetched {
		if lastErr == nil {
			lastErr = errors.New("no download source available")
		}
		return nil, lastErr
	}
	return versions, nil
}

// mcModrinthPickVersion 从版本列表中挑选第一个兼容 gameVersion/loader 且含文件的版本。
func mcModrinthPickVersion(versions []MCModrinthVersion, gameVersion, loader string) (*MCModrinthVersion, error) {
	for index := range versions {
		version := &versions[index]
		if !containsString(version.GameVersions, gameVersion) {
			continue
		}
		if loader != "" && !containsString(version.Loaders, loader) {
			continue
		}
		if len(version.Files) == 0 {
			continue
		}
		return version, nil
	}
	return nil, fmt.Errorf("该 mod 没有适配 %s 版本%s的文件", gameVersion, map[bool]string{true: "/" + loader, false: ""}[loader != ""])
}

// mcModrinthGetVersion 按版本 id 查询单个版本（用于依赖声明的固定 version_id）。
func mcModrinthGetVersion(ctx context.Context, versionID string) (*MCModrinthVersion, error) {
	endpoint := fmt.Sprintf("%s/version/%s", modrinthAPIBase, url.PathEscape(versionID))
	var version MCModrinthVersion
	var lastErr error
	fetched := false
	for _, candidate := range mcModrinthCandidates(endpoint) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate, nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("User-Agent", "PrismPanel/1.0")
		request.Header.Set("Accept", "application/json")
		response, err := mcHTTPClient.Do(request)
		if err != nil {
			lastErr = err
			if ctx.Err() == nil {
				mcHostMarkFailed(request.URL.Host)
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			if response.StatusCode >= 500 && ctx.Err() == nil {
				mcHostMarkFailed(request.URL.Host)
			}
			lastErr = fmt.Errorf("Modrinth 版本查询失败：%s", response.Status)
			continue
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&version)
		response.Body.Close()
		if decodeErr != nil {
			lastErr = decodeErr
			continue
		}
		fetched = true
		break
	}
	if !fetched {
		if lastErr == nil {
			lastErr = errors.New("no download source available")
		}
		return nil, lastErr
	}
	if len(version.Files) == 0 {
		return nil, fmt.Errorf("版本 %s 没有可下载的文件", versionID)
	}
	return &version, nil
}

// mcModrinthInstallVersionFile 下载单个版本的第一个文件到 mods 目录（覆盖已有同名文件，
// 按 Modrinth 提供的 size 校验）；installedFiles 按文件名去重（同一文件不重复下载）。
func mcModrinthInstallVersionFile(ctx context.Context, version *MCModrinthVersion, dir string, installedFiles map[string]bool) (string, error) {
	if version == nil || len(version.Files) == 0 {
		return "", errors.New("该版本没有可下载的文件")
	}
	file := version.Files[0]
	if installedFiles[file.Filename] {
		return filepath.Base(file.Filename), nil
	}
	target := filepath.Join(dir, filepath.Base(file.Filename))
	lastErr := error(nil)
	ok := false
	for _, candidate := range mcModrinthCandidates(file.URL) {
		if err := downloadURLOnceChecked(ctx, candidate, target, file.Size, ""); err == nil {
			ok = true
			break
		} else {
			lastErr = err
		}
	}
	if !ok {
		if lastErr == nil {
			lastErr = errors.New("no download source available")
		}
		return "", fmt.Errorf("mod 下载失败：%w", lastErr)
	}
	installedFiles[file.Filename] = true
	if report := filepath.Join(dir, filepath.Base(file.Filename)+".disabled"); fileExists(report) {
		_ = os.Remove(report)
	}
	return filepath.Base(file.Filename), nil
}

// MCModrinthInstall 安装 Modrinth 项目的最新兼容版本到指定版本的 mods 目录。
// #14：安装时解析版本 JSON 的 dependencies 字段，自动递归安装 required 依赖（去重、防环、
// 深度限制 5 层）；optional 依赖不装。任一依赖失败时返回明确错误（列出失败项）。
func MCModrinthInstall(ctx context.Context, versionID, projectID, gameVersion, loader string) (string, error) {
	dir, err := mcModsDir(versionID)
	if err != nil {
		return "", err
	}
	// 递归解析 required 依赖链：依赖在前、本体在后；visited 在一次安装内共享（去重/防环）
	items, err := mcModrinthResolveDependencies(ctx, mcModrinthFetchVersion, projectID, "", gameVersion, loader, map[string]bool{}, 0)
	if err != nil {
		return "", err
	}
	installedFiles := map[string]bool{}
	var targetFilename string
	var failed []string
	for _, item := range items {
		filename, err := mcModrinthInstallVersionFile(ctx, item.Version, dir, installedFiles)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s（%s）：%v", item.ProjectID, item.Version.VersionNumber, err))
			continue
		}
		if item.ProjectID == projectID {
			targetFilename = filename
		}
	}
	if len(failed) > 0 {
		return "", fmt.Errorf("mod/依赖安装失败：%s", strings.Join(failed, "；"))
	}
	if targetFilename == "" {
		return "", errors.New("目标 mod 未安装成功")
	}
	return targetFilename, nil
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
