package plugins

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

// maxModIconSize 限制 mod 图标读取大小（防止超大文件被当作图标整读）。
const maxModIconSize = 4 * 1024 * 1024

// Icon 返回制品 jar 内的 mod 图标（fabric.mod.json 的 icon 字段指向的条目）。
// 返回图标字节与 HTTP 内容类型；制品没有图标时返回 (nil, "", nil)。
// 图标路径优先取 manifest 中持久化的元数据；老 manifest 缺失时回退为
// 重新解析 jar 描述符。与 daemon 端无共享实现（daemon 不需要图标）。
func (r *Repository) Icon(pluginID string, artifactID int64, pluginTypes ...string) ([]byte, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pluginDir := filepath.Join(r.typeRoot(normalizePluginType(pluginTypes)), pluginID)
	manifest, err := r.loadManifestLocked(pluginDir, artifactID)
	if err != nil {
		return nil, "", err
	}
	iconPath := ""
	if manifest.ModMetadata != nil {
		iconPath = strings.TrimSpace(manifest.ModMetadata.Icon)
	}
	if iconPath == "" {
		contents, _, readErr := readFileLimited(filepath.Join(pluginDir, strconv.FormatInt(artifactID, 10), "plugin.jar"), maxPluginJARSize)
		if readErr != nil {
			return nil, "", readErr
		}
		_, primary, parseErr := ParseJAR(contents, manifest.PluginType)
		if parseErr != nil {
			return nil, "", parseErr
		}
		if primary.ModMetadata != nil {
			iconPath = strings.TrimSpace(primary.ModMetadata.Icon)
		}
	}
	if iconPath == "" {
		return nil, "", nil
	}
	return readIconEntry(filepath.Join(pluginDir, strconv.FormatInt(artifactID, 10), "plugin.jar"), iconPath)
}

func readIconEntry(jarPath, iconPath string) ([]byte, string, error) {
	reader, err := zip.OpenReader(jarPath)
	if err != nil {
		return nil, "", fmt.Errorf("open mod jar: %w", err)
	}
	defer reader.Close()
	normalized := strings.TrimPrefix(strings.ReplaceAll(iconPath, "\\", "/"), "/")
	var match *zip.File
	for _, entry := range reader.File {
		name := strings.TrimPrefix(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
		if name == normalized {
			match = entry
			break
		}
	}
	if match == nil {
		// 大小写不敏感回退（部分 mod 的图标路径大小写与 jar 内不一致）。
		for _, entry := range reader.File {
			name := strings.TrimPrefix(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
			if strings.EqualFold(name, normalized) {
				match = entry
				break
			}
		}
	}
	if match == nil {
		return nil, "", nil
	}
	if match.UncompressedSize64 > maxModIconSize {
		return nil, "", errors.New("mod icon exceeds 4 MiB")
	}
	stream, err := match.Open()
	if err != nil {
		return nil, "", err
	}
	defer stream.Close()
	contents, err := io.ReadAll(io.LimitReader(stream, maxModIconSize+1))
	if err != nil {
		return nil, "", err
	}
	if len(contents) == 0 || int64(len(contents)) > maxModIconSize {
		return nil, "", errors.New("mod icon exceeds 4 MiB")
	}
	return contents, iconContentType(match.Name, contents), nil
}

func iconContentType(name string, contents []byte) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	}
	if len(contents) >= 8 && bytes.Equal(contents[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
		return "image/png"
	}
	return "application/octet-stream"
}
