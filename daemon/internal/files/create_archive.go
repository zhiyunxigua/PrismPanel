package files

import (
	"archive/zip"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/atomicfile"
)

func (s *Service) Archive(target Target, source, destination string) (Entry, error) {
	if !s.acquireTransfer() {
		return Entry{}, apperr.New("TOO_MANY_REQUESTS", "文件传输并发数已达到上限")
	}
	defer s.releaseTransfer()

	var result Entry
	err := s.mutate(target, func(root string) error {
		sourceRelative, err := normalizeRelative(source)
		if err != nil || sourceRelative == "." {
			return apperr.New("INVALID_REQUEST", "必须指定压缩源路径")
		}
		destinationRelative, err := normalizeRelative(destination)
		if err != nil || destinationRelative == "." || !strings.EqualFold(path.Ext(destinationRelative), ".zip") {
			return apperr.New("INVALID_REQUEST", "压缩目标必须是 ZIP 文件")
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
			return apperr.New("FILE_EXISTS", "压缩目标已存在")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fileError(err, "无法检查压缩目标")
		}

		info, err := os.Lstat(sourcePath)
		if err != nil {
			return fileError(err, "压缩源读取失败")
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return apperr.New("INVALID_REQUEST", "压缩源不是受支持的文件或目录")
		}
		if info.IsDir() {
			relativeDestination, relErr := filepath.Rel(sourcePath, destinationPath)
			if relErr == nil && relativeDestination != "." && !strings.HasPrefix(relativeDestination, ".."+string(filepath.Separator)) && relativeDestination != ".." {
				return apperr.New("INVALID_REQUEST", "压缩目标不能位于压缩源目录内部")
			}
		}

		temp, err := os.CreateTemp(filepath.Dir(destinationPath), ".prism-archive-*.zip")
		if err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "无法创建压缩临时文件", err)
		}
		tempPath := temp.Name()
		defer os.Remove(tempPath)
		if err := temp.Chmod(0o640); err != nil {
			temp.Close()
			return apperr.Wrap("FILE_WRITE_FAILED", "无法设置压缩文件权限", err)
		}

		writer := zip.NewWriter(temp)
		archiveErr := addPathToArchive(writer, sourcePath, path.Base(sourceRelative), info)
		closeArchiveErr := writer.Close()
		syncErr := temp.Sync()
		closeTempErr := temp.Close()
		if err := errors.Join(archiveErr, closeArchiveErr, syncErr, closeTempErr); err != nil {
			if apiError := apperr.From(err); apiError.Code != "INTERNAL" {
				return err
			}
			return apperr.Wrap("FILE_OPERATION_FAILED", "文件压缩失败", err)
		}
		if err := atomicfile.Publish(tempPath, destinationPath, false); err != nil {
			if errors.Is(err, os.ErrExist) {
				return apperr.New("FILE_EXISTS", "压缩目标已存在")
			}
			return apperr.Wrap("FILE_WRITE_FAILED", "压缩文件发布失败", err)
		}
		created, err := os.Stat(destinationPath)
		if err != nil {
			return fileError(err, "无法读取压缩结果")
		}
		result = Entry{
			Name: path.Base(destinationRelative), Path: destinationRelative, Type: "file",
			Size: created.Size(), ModifiedAt: created.ModTime().UTC(), Mode: uint32(created.Mode().Perm()),
		}
		return nil
	})
	return result, err
}

func addPathToArchive(writer *zip.Writer, sourcePath, archiveName string, info os.FileInfo) error {
	if !info.IsDir() {
		return addDirectoryArchiveEntry(writer, filepath.Dir(sourcePath), "", sourcePath, fs.FileInfoToDirEntry(info), -1, new(int64))
	}
	entryCount := 0
	return filepath.WalkDir(sourcePath, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryCount++
		if entryCount > maxArchiveEntries {
			return apperr.New("FILE_TOO_LARGE", "目录条目数量超过压缩限制")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return apperr.New("PATH_ESCAPE", "目录包含不支持的符号链接")
		}
		return addDirectoryArchiveEntry(writer, sourcePath, archiveName, filePath, entry, -1, new(int64))
	})
}
