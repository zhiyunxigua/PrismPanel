package game

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CachePaths 游戏缓存目录布局（国际版下载/启动与历史路径共用）。
type CachePaths struct {
	Root      string
	Version   string
	Downloads string
	Base      string
	BaseMC    string
	Game      string
	GameMods  string
	Runtime   string
	Java      string
}

func DefaultCachePaths() (CachePaths, error) { return DefaultCachePathsForVersion("base") }

// DefaultCachePathsForVersion 返回指定版本标签的缓存目录。
func DefaultCachePathsForVersion(versionLabel string) (CachePaths, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return CachePaths{}, fmt.Errorf("resolve user cache directory: %w", err)
	}
	root = filepath.Join(root, "PrismPanel", "game-cache")
	versionLabel = strings.TrimSpace(versionLabel)
	if versionLabel == "" {
		versionLabel = "base"
	}
	versionDir := filepath.Join(root, safePathSegment(versionLabel))
	return CachePaths{
		Root: root, Version: versionDir, Downloads: filepath.Join(versionDir, "downloads"),
		Base: versionDir, BaseMC: filepath.Join(versionDir, ".minecraft"),
		Game: filepath.Join(root, "Game"), GameMods: filepath.Join(root, "GameMods"), Runtime: filepath.Join(root, "runtime"),
		Java: filepath.Join(root, "java"),
	}, nil
}

// EnsureInstanceDirectories 确保实例目录的 mods/config/resourcepacks/shaderpacks 存在。
func EnsureInstanceDirectories(instanceDir string) error { return EnsureModDirectories(instanceDir) }

// cleanArchivePath 清洗压缩包内路径，拒绝越界/绝对路径。
func cleanArchivePath(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return ".", nil
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || filepath.IsAbs(clean) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	return clean, nil
}

// pathWithin 判断 target 是否位于 root 之下。
func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
