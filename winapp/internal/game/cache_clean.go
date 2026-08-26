package game

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"PrismPanel-winapp/internal/credentials"
)

// 缓存清理（设置页 → 用户勾选 → 逐项删除）。
//
// 安全约束：每个可删除路径都必须校验在各自的白名单根目录之内（pathWithin），
// 只清理可重新生成的缓存 / 凭据 / 日志，绝不触碰游戏版本目录（.minecraft/versions）
// 与 mods 目录。

// CacheEntry 一项可清理缓存（设置页展示：名称 / 说明 / 路径 / 大小 / 存在性）。
type CacheEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	SizeText    string `json:"size_text,omitempty"` // 非字节类（凭据条数等）展示文本
	Exists      bool   `json:"exists"`
}

// CacheClearResult 单项清理结果。
type CacheClearResult struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

const (
	cacheIDMCAccount       = "mc-account"       // 国际版账号（正版/微软/离线/第三方认证）
	cacheIDPanelAccounts   = "panel-accounts"   // Prism 面板保存账号（当前面板 URL 范围）
	cacheIDGameCache       = "game-cache"       // 下载缓存 game-cache（UserCacheDir/PrismPanel/game-cache）
	cacheIDJavaRuntime     = "java-runtime"     // Java 运行时缓存（游戏目录/java）
	cacheIDDevLog          = "dev-log"          // 开发者日志 dev-mode.log
	cacheIDAuthlibInjector = "authlib-injector" // 第三方认证工具 jar
)

// ListCacheEntries 返回设置页可清理的缓存项清单（含大小与存在性）。
// panelAccounts 为面板凭据存储；panelURL 为空时面板账号项不可清理。
func ListCacheEntries(panelAccounts credentials.Store, panelURL string) []CacheEntry {
	return []CacheEntry{
		mcAccountCacheEntry(),
		panelAccountsCacheEntry(panelAccounts, panelURL),
		gameCacheEntry(),
		javaRuntimeCacheEntry(),
		authlibInjectorCacheEntry(),
		devLogCacheEntry(),
	}
}

// ClearCacheEntries 删除用户勾选的缓存项，逐项返回结果；未知 / 空 id 跳过并给出失败项。
func ClearCacheEntries(panelAccounts credentials.Store, panelURL string, ids []string) []CacheClearResult {
	results := make([]CacheClearResult, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		results = append(results, clearCacheEntry(panelAccounts, panelURL, id))
	}
	return results
}

func clearCacheEntry(panelAccounts credentials.Store, panelURL, id string) CacheClearResult {
	result := CacheClearResult{ID: id}
	var err error
	switch id {
	case cacheIDMCAccount:
		err = ClearMCCredential()
	case cacheIDPanelAccounts:
		if strings.TrimSpace(panelURL) == "" {
			err = errors.New("未配置面板地址，无法清理面板保存账号")
		} else {
			err = panelAccounts.ClearAll(panelURL)
		}
	case cacheIDGameCache:
		err = clearGameCacheRoot()
	case cacheIDJavaRuntime:
		err = clearJavaRuntimeCache()
	case cacheIDDevLog:
		err = DevLogClear()
	case cacheIDAuthlibInjector:
		err = clearAuthlibInjector()
	default:
		err = fmt.Errorf("未知缓存项：%s", id)
	}
	result.OK = err == nil
	if err != nil {
		result.Error = err.Error()
	}
	return result
}

// ClearMCCredential 删除本地国际版账号（Windows 凭据管理器 / mc-account.json）。
func ClearMCCredential() error { return NewMCLocalStore().Delete() }

// ---- 各项缓存解析与清理 ----

// mcAccountCacheEntry 国际版账号缓存项（OS 相关定位见 cache_clean_windows/other.go）。
func mcAccountCacheEntry() CacheEntry {
	entry := CacheEntry{
		ID:          cacheIDMCAccount,
		Name:        "国际版账号",
		Description: "正版 / 微软 / 离线 / 第三方认证登录的账号与令牌（清理后需重新登录）",
	}
	mcAccountCacheLocation(&entry)
	return entry
}

// panelAccountsCacheEntry Prism 面板保存账号（仅当前面板 URL 范围，不影响其他面板与游戏账号）。
func panelAccountsCacheEntry(panelAccounts credentials.Store, panelURL string) CacheEntry {
	entry := CacheEntry{
		ID:          cacheIDPanelAccounts,
		Name:        "Prism 面板保存账号",
		Description: "Windows 凭据管理器中当前面板的登录账号与自动登录标记（清理后需重新登录面板）",
		Path:        "Windows 凭据管理器（PrismPanel/<当前面板>/account/*）",
	}
	panelURL = strings.TrimSpace(panelURL)
	if panelURL == "" {
		entry.Exists = false
		entry.Description = "尚未配置面板地址，无法清理面板保存账号"
		return entry
	}
	accounts, err := panelAccounts.List(panelURL)
	if err != nil {
		entry.Description = "读取已保存账号失败：" + err.Error()
		return entry
	}
	if len(accounts) > 0 {
		entry.Exists = true
		entry.SizeText = fmt.Sprintf("%d 个账号", len(accounts))
	}
	return entry
}

// gameCacheEntry 下载缓存 game-cache（os.UserCacheDir()/PrismPanel/game-cache 整个目录）。
func gameCacheEntry() CacheEntry {
	entry := CacheEntry{
		ID:          cacheIDGameCache,
		Name:        "下载缓存（game-cache）",
		Description: "下载 / 启动产生的临时缓存与日志：downloads、Game、GameMods、runtime、java、logs（均可重新下载生成）",
	}
	paths, err := DefaultCachePaths()
	if err != nil {
		entry.Description = "无法定位用户缓存目录：" + err.Error()
		return entry
	}
	entry.Path = paths.Root
	entry.Exists, entry.SizeBytes = pathExistsSize(paths.Root)
	return entry
}

// javaRuntimeCacheEntry 自动下载的 Java 运行时缓存（游戏目录/java，重新下载恢复）。
func javaRuntimeCacheEntry() CacheEntry {
	entry := CacheEntry{
		ID:          cacheIDJavaRuntime,
		Name:        "Java 运行时缓存",
		Description: "启动器自动下载的 Java 运行时（缺失时下次启动自动重新下载）",
	}
	root, err := mcStoreRoot()
	if err != nil {
		entry.Description = "无法定位游戏目录：" + err.Error()
		return entry
	}
	dir := filepath.Join(root, "java")
	entry.Path = dir
	entry.Exists, entry.SizeBytes = pathExistsSize(dir)
	return entry
}

// authlibInjectorCacheEntry 第三方认证工具 jar（重新登录时自动下载）。
func authlibInjectorCacheEntry() CacheEntry {
	entry := CacheEntry{
		ID:          cacheIDAuthlibInjector,
		Name:        "第三方认证工具（authlib-injector.jar）",
		Description: "第三方认证服务器登录使用的 authlib-injector（下次使用时自动重新下载）",
	}
	jar, err := mcAuthlibInjectorJar()
	if err != nil {
		entry.Description = "无法定位工具文件：" + err.Error()
		return entry
	}
	entry.Path = jar
	entry.Exists, entry.SizeBytes = pathExistsSize(jar)
	return entry
}

// devLogCacheEntry 开发者日志文件（清空内存缓冲并删除日志文件）。
func devLogCacheEntry() CacheEntry {
	entry := CacheEntry{
		ID:          cacheIDDevLog,
		Name:        "开发者日志（dev-mode.log）",
		Description: "开发者模式记录的操作日志文件与内存缓冲",
	}
	path := DevLogPath()
	entry.Path = path
	if path != "" {
		entry.Exists, entry.SizeBytes = pathExistsSize(path)
	}
	return entry
}

// ---- 删除实现（均带白名单路径校验） ----

// safeRemovePath 删除白名单根目录内的文件 / 目录：
// 目标必须是 root 之下（pathWithin）且不等于 root 本身；越界直接拒绝。
func safeRemovePath(root, target string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if absTarget == absRoot {
		return errors.New("拒绝删除缓存根目录本身")
	}
	if !pathWithin(absRoot, absTarget) {
		return fmt.Errorf("路径不在允许的缓存目录内：%s", target)
	}
	return os.RemoveAll(absTarget)
}

// clearGameCacheRoot 删除整个 game-cache 目录（白名单根：UserCacheDir/PrismPanel）。
func clearGameCacheRoot() error {
	paths, err := DefaultCachePaths()
	if err != nil {
		return err
	}
	parent := filepath.Dir(paths.Root) // <UserCacheDir>/PrismPanel
	return safeRemovePath(parent, paths.Root)
}

// clearJavaRuntimeCache 删除游戏目录/java（白名单根：游戏目录）。
func clearJavaRuntimeCache() error {
	root, err := mcStoreRoot()
	if err != nil {
		return err
	}
	return safeRemovePath(root, filepath.Join(root, "java"))
}

// clearAuthlibInjector 删除游戏目录/authlib-injector.jar（白名单根：游戏目录）。
func clearAuthlibInjector() error {
	root, err := mcStoreRoot()
	if err != nil {
		return err
	}
	return safeRemovePath(root, filepath.Join(root, "authlib-injector.jar"))
}

// ---- 统计助手 ----

// pathExistsSize 返回路径存在性与大小（目录递归统计）。
func pathExistsSize(path string) (bool, int64) {
	info, err := os.Stat(path)
	if err != nil {
		return false, 0
	}
	if info.IsDir() {
		return true, dirSize(path)
	}
	return true, info.Size()
}

// dirSize 递归统计目录大小（跳过无法读取的条目）。
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}
