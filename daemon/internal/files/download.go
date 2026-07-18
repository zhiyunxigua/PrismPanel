package files

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"PrismPanel-daemon/internal/apperr"
)

func createDirectoryDownload(directory, name string) (*Download, error) {
	temp, err := os.CreateTemp("", ".prism-download-*.zip")
	if err != nil {
		return nil, apperr.Wrap("FILE_WRITE_FAILED", "无法创建目录压缩临时文件", err)
	}
	tempPath := temp.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	archive := zip.NewWriter(temp)
	entryCount := 0

	walkErr := filepath.WalkDir(directory, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryCount++
		if entryCount > maxArchiveEntries {
			return apperr.New("FILE_TOO_LARGE", "目录条目数量超过压缩下载限制")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return apperr.New("PATH_ESCAPE", "目录包含不支持的符号链接")
		}
		return addDirectoryArchiveEntry(archive, directory, name, filePath, entry)
	})
	return finishDirectoryDownload(temp, tempPath, name, cleanup, archive, walkErr)
}

func addDirectoryArchiveEntry(archive *zip.Writer, directory, name, filePath string, entry os.DirEntry) error {
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return apperr.New("INVALID_REQUEST", "目录包含不支持的文件类型")
	}
	relative, err := filepath.Rel(directory, filePath)
	if err != nil {
		return err
	}
	archiveName := name
	if relative != "." {
		archiveName = filepath.ToSlash(filepath.Join(name, relative))
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = strings.TrimPrefix(filepath.ToSlash(archiveName), "/")
	if info.IsDir() {
		header.Name = strings.TrimRight(header.Name, "/") + "/"
		header.Method = zip.Store
	} else {
		header.Method = zip.Deflate
	}
	output, err := archive.CreateHeader(header)
	if err != nil || info.IsDir() {
		return err
	}
	input, err := os.Open(filePath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := input.Close()
	return errors.Join(copyErr, closeErr)
}

func finishDirectoryDownload(
	temp *os.File,
	tempPath string,
	name string,
	cleanup func(),
	archive *zip.Writer,
	walkErr error,
) (*Download, error) {
	closeArchiveErr := archive.Close()
	syncErr := temp.Sync()
	closeTempErr := temp.Close()
	if err := errors.Join(walkErr, closeArchiveErr, syncErr, closeTempErr); err != nil {
		cleanup()
		if apiError := apperr.From(err); apiError.Code != "INTERNAL" {
			return nil, err
		}
		return nil, apperr.Wrap("FILE_OPERATION_FAILED", "目录压缩失败", err)
	}
	file, err := os.Open(tempPath)
	if err != nil {
		cleanup()
		return nil, apperr.Wrap("FILE_OPERATION_FAILED", "目录压缩结果无法打开", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		cleanup()
		return nil, apperr.Wrap("FILE_OPERATION_FAILED", "目录压缩结果无法读取", err)
	}
	return &Download{
		File: file, Name: name + ".zip", Size: info.Size(), Mode: info.ModTime().UTC(), cleanup: cleanup,
	}, nil
}
