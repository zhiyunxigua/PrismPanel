package deployment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
	"PrismPanel-daemon/internal/supervisor"
)

func (m *Manager) deployFiles(item *task, cfg model.ServerConfig, target supervisor.DeploymentTarget) (resultErr error) {
	root := filepath.Clean(cfg.RootPath)
	imagePath := filepath.Join(root, cfg.ImageDirectory)
	targetPath := filepath.Clean(target.Workspace)
	if filepath.Dir(targetPath) != root {
		return apperr.New("PATH_ESCAPE", "镜像实例目录不在镜像根目录内")
	}
	tempPath := filepath.Join(root, ".deploy-"+target.InstanceID+"-"+item.TaskID)
	backupPath := filepath.Join(root, ".backup-"+target.InstanceID+"-"+item.TaskID)
	if pathExists(tempPath) || pathExists(backupPath) {
		return apperr.New("ROLLBACK_FAILED", "部署临时目录或备份目录已存在")
	}
	if err := os.Mkdir(tempPath, 0o750); err != nil {
		return apperr.Wrap("INTERNAL", "无法创建部署临时目录", err)
	}
	defer func() {
		if pathExists(tempPath) {
			if err := safeRemoveAll(root, tempPath); resultErr == nil && err != nil {
				resultErr = err
			}
		}
	}()

	m.log(item, "info", "copying_image", target.InstanceID, "正在复制镜像内容")
	files, bytes, err := m.copyTree(item.context, imagePath, tempPath, func(relative string, entry fs.DirEntry) bool {
		return isExcluded(relative, entry.IsDir(), cfg.Exclude)
	})
	if err != nil {
		return err
	}
	m.log(item, "info", "copying_image", target.InstanceID, fmt.Sprintf("镜像复制完成：%d 个文件，%d 字节", files, bytes))

	m.log(item, "info", "restoring_excluded", target.InstanceID, "正在恢复排除项")
	if err := m.restoreExcluded(item.context, targetPath, tempPath, cfg.Exclude); err != nil {
		return err
	}
	if err := supervisor.WriteServerPort(tempPath, target.Port); err != nil {
		return apperr.Wrap("INTERNAL", "无法写入实例端口", err)
	}
	if err := item.context.Err(); err != nil {
		return err
	}

	m.log(item, "info", "swapping", target.InstanceID, "正在交换实例目录")
	hadOriginal := pathExists(targetPath)
	if hadOriginal {
		if err := os.Rename(targetPath, backupPath); err != nil {
			return apperr.Wrap("INTERNAL", "无法创建实例目录备份", err)
		}
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		if hadOriginal {
			if rollbackErr := os.Rename(backupPath, targetPath); rollbackErr != nil {
				return apperr.Wrap("ROLLBACK_FAILED", "部署失败且无法恢复原实例目录", errors.Join(err, rollbackErr))
			}
		}
		return apperr.Wrap("INTERNAL", "无法启用新的实例目录", err)
	}
	if hadOriginal {
		if err := safeRemoveAll(root, backupPath); err != nil {
			return apperr.Wrap("ROLLBACK_FAILED", "新目录已启用，但旧目录备份清理失败", err)
		}
	}
	return nil
}

func (m *Manager) copyTree(
	ctx context.Context,
	sourceRoot string,
	destinationRoot string,
	skip func(relative string, entry fs.DirEntry) bool,
) (int64, int64, error) {
	var fileCount int64
	var byteCount int64
	err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if skip != nil && skip(relative, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in deployment: %s", relative)
		}
		destination := filepath.Join(destinationRoot, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
				return err
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in deployment: %s", relative)
		}
		copied, err := copyFile(ctx, path, destination, info)
		if err != nil {
			return err
		}
		fileCount++
		byteCount += copied
		return nil
	})
	return fileCount, byteCount, err
}

func (m *Manager) restoreExcluded(ctx context.Context, oldRoot, destinationRoot string, entries []model.ExcludeEntry) error {
	for _, excluded := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		source := filepath.Join(oldRoot, excluded.Path)
		info, err := os.Lstat(source)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if excluded.Type == "directory" && !info.IsDir() {
			return fmt.Errorf("excluded path must be a directory: %s", excluded.Path)
		}
		if excluded.Type == "file" && !info.Mode().IsRegular() {
			return fmt.Errorf("excluded path must be a regular file: %s", excluded.Path)
		}
		destination := filepath.Join(destinationRoot, excluded.Path)
		if info.IsDir() {
			if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
				return err
			}
			if _, _, err := m.copyTree(ctx, source, destination, nil); err != nil {
				return err
			}
			continue
		}
		if _, err := copyFile(ctx, source, destination, info); err != nil {
			return err
		}
	}
	return nil
}

func isExcluded(relative string, directory bool, entries []model.ExcludeEntry) bool {
	clean := filepath.Clean(relative)
	for _, excluded := range entries {
		excludedPath := filepath.Clean(excluded.Path)
		if clean == excludedPath {
			return true
		}
		if excluded.Type == "directory" && strings.HasPrefix(clean, excludedPath+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func copyFile(ctx context.Context, source, destination string, info fs.FileInfo) (written int64, resultErr error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return 0, err
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer sourceFile.Close()
	destinationFile, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return 0, err
	}
	defer func() {
		closeErr := destinationFile.Close()
		if resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
		if resultErr != nil {
			_ = os.Remove(destination)
		}
	}()
	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		count, readErr := sourceFile.Read(buffer)
		if count > 0 {
			wrote, writeErr := destinationFile.Write(buffer[:count])
			written += int64(wrote)
			if writeErr != nil {
				return written, writeErr
			}
			if wrote != count {
				return written, errors.New("short deployment file write")
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return written, readErr
		}
	}
	if err := destinationFile.Sync(); err != nil {
		return written, err
	}
	if err := os.Chtimes(destination, time.Now(), info.ModTime()); err != nil {
		return written, err
	}
	return written, nil
}

func safeRemoveAll(root, target string) error {
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	if filepath.Dir(cleanTarget) != cleanRoot {
		return apperr.New("PATH_ESCAPE", "拒绝删除镜像根目录外的部署临时目录")
	}
	base := filepath.Base(cleanTarget)
	if !strings.HasPrefix(base, ".deploy-") && !strings.HasPrefix(base, ".backup-") {
		return apperr.New("PATH_ESCAPE", "拒绝删除非部署临时目录")
	}
	return os.RemoveAll(cleanTarget)
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
