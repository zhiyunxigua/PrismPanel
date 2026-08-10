package files

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/atomicfile"
)

const maxCopyEntries = 100000

// Copy duplicates a regular file or directory inside one file target.
// The destination is published only after the complete copy succeeds.
func (s *Service) Copy(target Target, source, destination string) (Entry, error) {
	if !s.acquireTransfer() {
		return Entry{}, apperr.New("TOO_MANY_REQUESTS", "文件传输并发数已达到上限")
	}
	defer s.releaseTransfer()

	var result Entry
	err := s.mutate(target, func(root string) error {
		sourceRelative, err := normalizeRelative(source)
		if err != nil || sourceRelative == "." {
			return apperr.New("INVALID_REQUEST", "必须指定复制源路径")
		}
		destinationRelative, err := normalizeRelative(destination)
		if err != nil || destinationRelative == "." {
			return apperr.New("INVALID_REQUEST", "必须指定复制目标路径")
		}
		sourcePath, err := securePath(root, sourceRelative, false)
		if err != nil {
			return err
		}
		destinationPath, err := securePath(root, destinationRelative, true)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(destinationPath); err == nil {
			return apperr.New("FILE_EXISTS", "复制目标已存在")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fileError(err, "无法检查复制目标")
		}

		info, err := os.Lstat(sourcePath)
		if err != nil {
			return fileError(err, "复制源读取失败")
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return apperr.New("INVALID_REQUEST", "复制源不是受支持的文件或目录")
		}
		if info.IsDir() {
			relativeDestination, relErr := filepath.Rel(sourcePath, destinationPath)
			if relErr == nil && relativeDestination != "." &&
				!strings.HasPrefix(relativeDestination, ".."+string(filepath.Separator)) &&
				relativeDestination != ".." {
				return apperr.New("INVALID_REQUEST", "复制目标不能位于复制源目录内部")
			}
		}

		if info.IsDir() {
			if err := copyDirectory(sourcePath, destinationPath); err != nil {
				return err
			}
		} else if err := copyFile(sourcePath, destinationPath, info.Mode().Perm()); err != nil {
			return err
		}
		result = Entry{
			Name: path.Base(destinationRelative), Path: destinationRelative,
			Type: copiedEntryType(info), Size: info.Size(), ModifiedAt: info.ModTime().UTC(),
			Mode: uint32(info.Mode().Perm()),
		}
		if updated, statErr := os.Stat(destinationPath); statErr == nil {
			result.Size = updated.Size()
			result.ModifiedAt = updated.ModTime().UTC()
		}
		return nil
	})
	return result, err
}

func copiedEntryType(info os.FileInfo) string {
	if info.IsDir() {
		return "directory"
	}
	return "file"
}

func copyFile(source, destination string, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(destination), ".prism-copy-*")
	if err != nil {
		return apperr.Wrap("FILE_WRITE_FAILED", "无法创建复制临时文件", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return apperr.Wrap("FILE_WRITE_FAILED", "无法设置复制文件权限", err)
	}
	input, err := os.Open(source)
	if err != nil {
		_ = temp.Close()
		return fileError(err, "复制源打开失败")
	}
	_, copyErr := io.Copy(temp, input)
	closeInputErr := input.Close()
	syncErr := temp.Sync()
	closeTempErr := temp.Close()
	if err := errors.Join(copyErr, closeInputErr, syncErr, closeTempErr); err != nil {
		return apperr.Wrap("FILE_WRITE_FAILED", "文件复制失败", err)
	}
	if err := atomicfile.Publish(tempPath, destination, false); err != nil {
		if errors.Is(err, os.ErrExist) {
			return apperr.New("FILE_EXISTS", "复制目标已存在")
		}
		return apperr.Wrap("FILE_WRITE_FAILED", "复制文件发布失败", err)
	}
	return nil
}

func copyDirectory(source, destination string) error {
	if err := os.Mkdir(destination, 0o750); err != nil {
		return apperr.Wrap("FILE_WRITE_FAILED", "无法创建复制目录", err)
	}
	clean := true
	defer func() {
		if clean {
			_ = os.RemoveAll(destination)
		}
	}()
	entries := 0
	err := filepath.WalkDir(source, func(sourcePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > maxCopyEntries {
			return apperr.New("FILE_TOO_LARGE", "复制目录条目数量超过限制")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return apperr.New("PATH_ESCAPE", "目录包含不支持的符号链接")
		}
		if sourcePath == source {
			return nil
		}
		relative, err := filepath.Rel(source, sourcePath)
		if err != nil {
			return err
		}
		destinationPath := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := os.Mkdir(destinationPath, info.Mode().Perm()); err != nil {
				return err
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return apperr.New("INVALID_REQUEST", "目录包含不支持的文件类型")
		}
		return copyFile(sourcePath, destinationPath, info.Mode().Perm())
	})
	if err != nil {
		if apiError := apperr.From(err); apiError.Code != "INTERNAL" {
			return err
		}
		return apperr.Wrap("FILE_WRITE_FAILED", "目录复制失败", err)
	}
	clean = false
	return nil
}
