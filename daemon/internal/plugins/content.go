package plugins

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ContentDeployStats 是内容包部署的统计结果（供前端展示与完全配置备份回滚提示）。
type ContentDeployStats struct {
	Applied     int    `json:"applied"`
	Overwritten int    `json:"overwritten"`
	Added       int    `json:"added"`
	BackupPath  string `json:"backup_path,omitempty"`
}

type deployContentOptions struct {
	BackupSnapshot bool
	BackupDir      string
}

// deployContentToWorkspace 将内容包部署到 <workspace>/：
// zip 顶层即工作目录结构（config/→config/、plugins/x/config.json→plugins/x/config.json…），
// 覆盖策略 = 覆盖同名 + 保留额外（不删除目标额外文件）。
// 事务式：内容已在临时目录完整解压校验（prepareBundle），逐文件覆盖，覆盖前将同名文件
// 备份到事务标记文件（destination+txn+.backup），失败按逆序回滚已覆盖文件。
func deployContentToWorkspace(workspace string, bundle *preparedBundle, options deployContentOptions) (ContentDeployStats, error) {
	stats := ContentDeployStats{}
	if bundle.manifest.Kind != "content" {
		return stats, errors.New("content deployment requires a content bundle")
	}
	if options.BackupSnapshot {
		if options.BackupDir == "" {
			return stats, errors.New("backup directory is not configured")
		}
		backupPath, err := snapshotWorkspace(workspace, options.BackupDir)
		if err != nil {
			return stats, fmt.Errorf("create workspace snapshot backup: %w", err)
		}
		stats.BackupPath = backupPath
	}
	if err := ensureWorkspace(workspace); err != nil {
		return stats, err
	}
	txn := fmt.Sprintf(".prism-content-%d", time.Now().UnixNano())
	rollbacks := make([]func(), 0)
	rollback := func() {
		for index := len(rollbacks) - 1; index >= 0; index-- {
			rollbacks[index]()
		}
	}
	err := filepath.WalkDir(bundle.contentPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(bundle.contentPath, path)
		if relErr != nil {
			return relErr
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if relative == "manifest.yaml" {
			return nil
		}
		destination, err := workspacePath(workspace, relative)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o750)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("content bundle contains unsupported entry %s", relative)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return err
		}
		temp := destination + txn + ".new"
		backup := destination + txn + ".backup"
		if err := copyFile(path, temp); err != nil {
			return err
		}
		hadTarget := pathExists(destination)
		if hadTarget {
			if err := os.Rename(destination, backup); err != nil {
				_ = os.Remove(temp)
				return err
			}
			stats.Overwritten++
		} else {
			stats.Added++
		}
		if err := os.Rename(temp, destination); err != nil {
			if hadTarget {
				_ = os.Rename(backup, destination)
			}
			return err
		}
		stats.Applied++
		rollbacks = append(rollbacks, func() {
			_ = os.Remove(destination)
			if hadTarget {
				_ = os.Rename(backup, destination)
			}
		})
		return nil
	})
	if err != nil {
		rollback()
		if stats.BackupPath != "" {
			_ = os.Remove(stats.BackupPath)
			stats.BackupPath = ""
		}
		return stats, err
	}
	cleanupContentTransactionFiles(workspace, txn)
	return stats, nil
}

// workspacePath 将内容包相对路径安全地拼接到工作目录下（防路径逃逸，与 cleanBundlePath 同强度）。
func workspacePath(workspace, relative string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if clean == "." || filepath.IsAbs(filepath.FromSlash(clean)) || clean == ".." ||
		strings.HasPrefix(clean, "../") || strings.ContainsRune(clean, 0) {
		return "", fmt.Errorf("content path escapes workspace: %s", relative)
	}
	path := filepath.Join(workspace, filepath.FromSlash(clean))
	rel, err := filepath.Rel(workspace, path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("content path escapes workspace: %s", relative)
	}
	return path, nil
}

// cleanupContentTransactionFiles 清理工作目录下内容包部署事务的残留临时文件（.new/.backup）。
func cleanupContentTransactionFiles(workspace, marker string) {
	_ = filepath.WalkDir(workspace, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.Contains(entry.Name(), marker) {
			_ = os.Remove(path)
		}
		return nil
	})
}

// snapshotWorkspace 将整个工作目录打成 zip 快照（完全配置部署前备份，供回滚），返回备份路径。
func snapshotWorkspace(workspace, backupDir string) (string, error) {
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return "", err
	}
	name := sanitizeBackupName(filepath.Base(workspace))
	path := filepath.Join(backupDir, fmt.Sprintf("workspace-%s-%d.zip", name, time.Now().UnixNano()))
	if err := zipDirectory(workspace, path); err != nil {
		return "", err
	}
	return path, nil
}

func sanitizeBackupName(value string) string {
	value = strings.Map(func(char rune) rune {
		switch char {
		case '<', '>', ':', 34, '/', 92, '|', '?', '*':
			return '-'
		}
		if char < 32 {
			return '-'
		}
		return char
	}, value)
	if value == "" || value == "." || value == ".." {
		return "workspace"
	}
	return value
}

// zipDirectory 将 source 目录的全部内容压缩到 destPath（zip 顶层 = source 内容）。
func zipDirectory(source, destPath string) error {
	output, err := os.Create(destPath)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	walkErr := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || path == source {
			return walkErr
		}
		relative, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		name := filepath.ToSlash(relative)
		if entry.IsDir() {
			_, err := archive.Create(name + "/")
			return err
		}
		// 快照只收普通文件；符号链接/特殊条目跳过（与内容包校验强度一致）。
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		header, headerErr := zip.FileInfoHeader(info)
		if headerErr != nil {
			return headerErr
		}
		header.Name = name
		header.Method = zip.Deflate
		writer, createErr := archive.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		input, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		_, copyErr := io.Copy(writer, input)
		input.Close()
		return copyErr
	})
	closeErr := archive.Close()
	output.Close()
	if walkErr != nil {
		_ = os.Remove(destPath)
		return walkErr
	}
	if closeErr != nil {
		_ = os.Remove(destPath)
		return closeErr
	}
	return nil
}
