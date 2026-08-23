package game

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// ServerConfig 启动一台游戏所需的基本信息（国际版启动器使用）。
type ServerConfig struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	GameID       string    `json:"game_id,omitempty"`
	IP           string    `json:"ip"`
	Port         int       `json:"port"`
	Username     string    `json:"username"`
	VersionLabel string    `json:"version_label"`
	ModDir       string    `json:"mod_dir"`
	CreatedAt    time.Time `json:"created_at"`
}

// JoinPreparation 启动前准备好的运行目录信息。
type JoinPreparation struct {
	Server     ServerConfig `json:"server"`
	VersionDir string       `json:"version_dir"`
	RuntimeDir string       `json:"runtime_dir"`
	GameDir    string       `json:"game_dir"`
}

// fileExists 判断路径是否为存在的普通文件。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// directoryExists 判断路径是否为存在的目录。
func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// EnsureModDirectories 确保 mods/config/resourcepacks/shaderpacks 目录存在。
func EnsureModDirectories(modDir string) error {
	modDir = strings.TrimSpace(modDir)
	if modDir == "" {
		return errors.New("mod directory is required")
	}
	for _, name := range []string{"mods", "config", "resourcepacks", "shaderpacks"} {
		if err := os.MkdirAll(filepath.Join(modDir, name), 0o755); err != nil {
			return fmt.Errorf("create mod directory %s: %w", name, err)
		}
	}
	return nil
}

// copyDirectory 递归复制目录。
func copyDirectory(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", source)
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(target, 0o755)
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		return copyFile(path, destination)
	})
}

// copyFile 复制单个文件（自动创建目标目录）。
func copyFile(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

var unsafePathSegment = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// safePathSegment 把任意字符串清洗为安全的路径段。
func safePathSegment(value string) string {
	cleaned := strings.Trim(unsafePathSegment.ReplaceAllString(value, "-"), "-._")
	if cleaned == "" {
		return "server"
	}
	return cleaned
}

// safeRuntimeSegment 清洗运行目录段（保留字母数字与 . _ -）。
func safeRuntimeSegment(value string) string {
	cleaned := strings.Map(func(char rune) rune {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '.' || char == '_' || char == '-' {
			return char
		}
		return '-'
	}, strings.TrimSpace(value))
	cleaned = strings.Trim(cleaned, "-._")
	if cleaned == "" {
		return "game"
	}
	return cleaned
}
