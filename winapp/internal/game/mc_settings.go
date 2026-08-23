package game

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// MCLauncherSettings 全局下载/启动设置（保存在 minecraft/settings.json）。
type MCLauncherSettings struct {
	Concurrency     int    `json:"concurrency"`       // 并发下载数 1-32，默认 8
	Mirror          string `json:"mirror"`            // ""/auto 自动、bmclapi、off、或自定义 http 镜像
	GameDir         string `json:"game_dir"`          // 游戏（版本存储）根目录，默认 <程序目录>/minecraft
	DefaultJava     string `json:"default_java"`      // 默认 Java 可执行文件路径（可选）
	DefaultMemoryMB int    `json:"default_memory_mb"` // 默认分配内存 MB（可选）
	DevMode         bool   `json:"dev_mode"`          // 开发者模式：记录所有操作到 dev-mode.log
}

var (
	mcSettingsMu     sync.RWMutex
	mcSettingsLoaded bool
	mcSettingsCache  MCLauncherSettings
)

// baseStoreRoot 返回存储根目录（环境变量覆盖优先，其次程序目录下的 minecraft/）。
func baseStoreRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv("PRISMPANEL_MC_DIR")); value != "" {
		return filepath.Abs(value)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), "minecraft"), nil
}

// launcherSettingsPath 返回全局设置文件路径（基于基础根目录，避免依赖设置本身导致递归）。
func launcherSettingsPath() (string, error) {
	root, err := baseStoreRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "settings.json"), nil
}

// settingsPathForRoot 根据目标游戏目录计算设置文件路径（保存设置时使用，写入新根目录）。
func settingsPathForRoot(gameDir string) (string, error) {
	if value := strings.TrimSpace(os.Getenv("PRISMPANEL_MC_DIR")); value != "" {
		root, err := filepath.Abs(value)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "settings.json"), nil
	}
	if strings.TrimSpace(gameDir) != "" {
		root, err := filepath.Abs(strings.TrimSpace(gameDir))
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "settings.json"), nil
	}
	root, err := baseStoreRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "settings.json"), nil
}

// LoadMCSettings 读取全局下载/启动设置。
func LoadMCSettings() (MCLauncherSettings, error) {
	path, err := launcherSettingsPath()
	if err != nil {
		return MCLauncherSettings{}, err
	}
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return MCLauncherSettings{}, nil
	}
	if err != nil {
		return MCLauncherSettings{}, err
	}
	var settings MCLauncherSettings
	if err := json.Unmarshal(contents, &settings); err != nil {
		return MCLauncherSettings{}, err
	}
	return settings, nil
}

// SaveMCSettings 保存全局设置并刷新缓存（写入生效后的游戏目录根）。
func SaveMCSettings(settings MCLauncherSettings) error {
	if settings.Concurrency < 1 {
		settings.Concurrency = 16
	}
	if settings.Concurrency > 64 {
		settings.Concurrency = 64
	}
	path, err := settingsPathForRoot(settings.GameDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return err
	}
	mcSettingsMu.Lock()
	mcSettingsCache = settings
	mcSettingsLoaded = true
	mcSettingsMu.Unlock()
	return nil
}

func loadCachedSettings() (MCLauncherSettings, bool) {
	mcSettingsMu.RLock()
	if mcSettingsLoaded {
		s := mcSettingsCache
		mcSettingsMu.RUnlock()
		return s, true
	}
	mcSettingsMu.RUnlock()
	settings, err := LoadMCSettings()
	if err != nil {
		return MCLauncherSettings{}, false
	}
	mcSettingsMu.Lock()
	mcSettingsCache = settings
	mcSettingsLoaded = true
	mcSettingsMu.Unlock()
	return settings, true
}

// mcEffectiveConcurrency 返回有效并发下载数（1-64，默认 16；参照 PCL2 默认多线程下载）。
func mcEffectiveConcurrency() int {
	if settings, ok := loadCachedSettings(); ok && settings.Concurrency >= 1 {
		if settings.Concurrency > 64 {
			return 64
		}
		return settings.Concurrency
	}
	return 16
}

// versionSettingsPath 返回指定版本的独立设置文件路径。
func versionSettingsPath(versionID string) (string, error) {
	root, err := mcStoreRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, safePathSegment(versionID), "settings.json"), nil
}

// LoadMCVersionSettings 读取版本特定设置；不存在时返回 ok=false。
func LoadMCVersionSettings(versionID string) (MCVersionSettings, bool, error) {
	path, err := versionSettingsPath(versionID)
	if err != nil {
		return MCVersionSettings{}, false, err
	}
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return MCVersionSettings{}, false, nil
	}
	if err != nil {
		return MCVersionSettings{}, false, err
	}
	var settings MCVersionSettings
	if err := json.Unmarshal(contents, &settings); err != nil {
		return MCVersionSettings{}, false, err
	}
	return settings, true, nil
}

// SaveMCVersionSettings 保存版本特定设置。
func SaveMCVersionSettings(versionID string, settings MCVersionSettings) error {
	path, err := versionSettingsPath(versionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0o644)
}

// DeleteMCVersionSettings 删除版本设置文件（版本删除时调用）。
func DeleteMCVersionSettings(versionID string) error {
	path, err := versionSettingsPath(versionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
