package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type cacheKey struct {
	Path       string
	PluginType string
	Size       int64
	ModifiedNS int64
}

type scanCache struct {
	mu    sync.Mutex
	items map[cacheKey]FilePlugin
}

func newScanCache() *scanCache {
	return &scanCache{items: make(map[cacheKey]FilePlugin)}
}

// scan 扫描 workspace/plugins 目录（Bukkit 系插件的固定目录），保持既有调用兼容。
func (c *scanCache) scan(workspace string, pluginTypes ...string) ([]FilePlugin, []string) {
	return c.scanDirectory(workspace, "plugins", pluginTypes...)
}

// scanMods 扫描 workspace/mods 目录（Fabric/Forge 模组目录）。
func (c *scanCache) scanMods(workspace string, modTypes ...string) ([]FilePlugin, []string) {
	return c.scanDirectory(workspace, "mods", modTypes...)
}

// scanDirectory 泛化目录扫描：识别 <workspace>/<directory> 下的 .jar 与 .jar.disabled。
func (c *scanCache) scanDirectory(workspace, directory string, pluginTypes ...string) ([]FilePlugin, []string) {
	pluginType := requestedPluginType(pluginTypes)
	pluginDir := filepath.Join(workspace, directory)
	entries, err := os.ReadDir(pluginDir)
	if os.IsNotExist(err) {
		return []FilePlugin{}, []string{}
	}
	if err != nil {
		return []FilePlugin{}, []string{fmt.Sprintf("read %s directory: %v", directory, err)}
	}
	items := make([]FilePlugin, 0)
	warnings := make([]string, 0)
	live := make(map[cacheKey]struct{})
	for _, entry := range entries {
		name := entry.Name()
		lower := strings.ToLower(name)
		enabled := strings.HasSuffix(lower, ".jar")
		disabled := strings.HasSuffix(lower, ".jar.disabled")
		if entry.IsDir() || (!enabled && !disabled) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			warnings = append(warnings, fmt.Sprintf("skip %s: not a regular file", name))
			continue
		}
		path := filepath.Join(pluginDir, name)
		key := cacheKey{Path: path, PluginType: pluginType, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano()}
		live[key] = struct{}{}
		c.mu.Lock()
		cached, exists := c.items[key]
		c.mu.Unlock()
		if exists {
			items = append(items, cached)
			continue
		}
		plugin, scanErr := scanFile(path, name, enabled, info, pluginType)
		if scanErr != nil {
			warnings = append(warnings, fmt.Sprintf("scan %s: %v", name, scanErr))
			continue
		}
		c.mu.Lock()
		c.items[key] = plugin
		c.mu.Unlock()
		items = append(items, plugin)
	}
	c.mu.Lock()
	for key := range c.items {
		if _, exists := live[key]; !exists && strings.HasPrefix(key.Path, pluginDir+string(filepath.Separator)) {
			delete(c.items, key)
		}
	}
	c.mu.Unlock()
	sort.Slice(items, func(left, right int) bool {
		if strings.EqualFold(items[left].Name, items[right].Name) {
			return strings.ToLower(items[left].SourceFile) < strings.ToLower(items[right].SourceFile)
		}
		return strings.ToLower(items[left].Name) < strings.ToLower(items[right].Name)
	})
	return items, warnings
}

func scanFile(path, sourceFile string, enabled bool, info os.FileInfo, pluginTypes ...string) (FilePlugin, error) {
	pluginType := requestedPluginType(pluginTypes)
	descriptors, primary, err := parseJAR(path, pluginType)
	if err != nil {
		return FilePlugin{}, err
	}
	hash, err := hashFile(path)
	if err != nil {
		return FilePlugin{}, err
	}
	mainClass := primary.Main
	if mainClass == "" {
		mainClass = primary.Bootstrapper
	}
	return FilePlugin{
		PluginType: primary.PluginType, ID: primary.ID, Name: primary.Name, Version: primary.Version,
		Main:    mainClass,
		Authors: append([]string(nil), primary.Authors...), Description: primary.Description,
		Website: primary.Website, Dependencies: append([]string(nil), primary.Dependencies...),
		Descriptors: descriptors, SourceFile: sourceFile, SHA256: hash,
		Size: info.Size(), ModifiedAt: info.ModTime().UTC(), Enabled: enabled,
	}, nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// scanEnabledPluginHashes 计算 workspace/plugins 目录下已启用 jar 的哈希基线。
func scanEnabledPluginHashes(workspace string) (map[string]string, error) {
	return scanEnabledHashes(workspace, "plugins")
}

// scanEnabledModHashes 计算 workspace/mods 目录下已启用 jar 的哈希基线。
func scanEnabledModHashes(workspace string) (map[string]string, error) {
	return scanEnabledHashes(workspace, "mods")
}

// scanEnabledHashes 泛化目录哈希基线：只统计目录顶层、非符号链接的 .jar 文件。
func scanEnabledHashes(workspace, directory string) (map[string]string, error) {
	pluginDir := filepath.Join(workspace, directory)
	entries, err := os.ReadDir(pluginDir)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".jar") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, infoErr
		}
		if !info.Mode().IsRegular() {
			continue
		}
		digest, hashErr := hashFile(filepath.Join(pluginDir, name))
		if hashErr != nil {
			return nil, hashErr
		}
		result[strings.ToLower(name)] = digest
	}
	return result, nil
}
