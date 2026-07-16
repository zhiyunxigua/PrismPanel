package plugins

import (
	"crypto/sha256"
	"encoding/hex"
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

func (c *scanCache) scan(workspace string) ([]FilePlugin, []string) {
	pluginDir := filepath.Join(workspace, "plugins")
	entries, err := os.ReadDir(pluginDir)
	if os.IsNotExist(err) {
		return []FilePlugin{}, []string{}
	}
	if err != nil {
		return []FilePlugin{}, []string{fmt.Sprintf("read plugins directory: %v", err)}
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
		key := cacheKey{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano()}
		live[key] = struct{}{}
		c.mu.Lock()
		cached, exists := c.items[key]
		c.mu.Unlock()
		if exists {
			items = append(items, cached)
			continue
		}
		plugin, scanErr := scanFile(path, name, enabled, info)
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

func scanFile(path, sourceFile string, enabled bool, info os.FileInfo) (FilePlugin, error) {
	descriptors, primary, err := parseJAR(path)
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
		Name: primary.Name, Version: primary.Version, Main: mainClass,
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
