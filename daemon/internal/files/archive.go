package files

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"PrismPanel-daemon/internal/apperr"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const maxArchiveEntries = 100000

type ImportResult struct {
	Files       int   `json:"files"`
	Directories int   `json:"directories"`
	Bytes       int64 `json:"bytes"`
}

type archiveEntry struct {
	file *zip.File
	path string
}

func (s *Service) ImportArchive(target Target, relative string, source io.Reader, expectedSize int64, expectedSHA string) (ImportResult, error) {
	clean, err := normalizeRelative(relative)
	if err != nil || clean != "." {
		return ImportResult{}, apperr.New("INVALID_REQUEST", "压缩包只能导入工作目录根路径")
	}
	if expectedSize <= 0 || expectedSize > s.maxUpload {
		return ImportResult{}, apperr.New("FILE_TOO_LARGE", "压缩包超过节点上传限制")
	}
	if !s.acquireTransfer() {
		return ImportResult{}, apperr.New("TOO_MANY_REQUESTS", "文件传输并发数已达到上限")
	}
	defer s.releaseTransfer()

	var result ImportResult
	err = s.mutateStopped(target, func(root string) error {
		var err error
		result, err = s.importArchive(root, source, expectedSize, expectedSHA)
		return err
	})
	return result, err
}

func (s *Service) importArchive(root string, source io.Reader, expectedSize int64, expectedSHA string) (ImportResult, error) {
	empty, err := directoryEmpty(root)
	if err != nil {
		return ImportResult{}, err
	}
	if !empty {
		return ImportResult{}, apperr.New("DIRECTORY_NOT_EMPTY", "仅允许向空工作目录导入压缩包")
	}
	parent := filepath.Dir(root)
	if sameFilePath(parent, root) {
		return ImportResult{}, apperr.New("INVALID_CONFIG", "不能将文件系统根目录作为压缩包导入目标")
	}

	upload, err := os.CreateTemp(parent, ".prism-archive-*.zip")
	if err != nil {
		return ImportResult{}, apperr.Wrap("FILE_WRITE_FAILED", "无法创建压缩包临时文件", err)
	}
	uploadPath := upload.Name()
	defer os.Remove(uploadPath)

	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(upload, hash), io.LimitReader(source, s.maxUpload+1))
	syncErr := upload.Sync()
	if copyErr != nil || syncErr != nil {
		upload.Close()
		return ImportResult{}, apperr.Wrap("FILE_WRITE_FAILED", "压缩包上传失败", errors.Join(copyErr, syncErr))
	}
	if written > s.maxUpload || written != expectedSize {
		upload.Close()
		return ImportResult{}, apperr.New("FILE_SIZE_MISMATCH", "压缩包大小与授权不一致")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if expectedSHA != "" && !strings.EqualFold(expectedSHA, digest) {
		upload.Close()
		return ImportResult{}, apperr.New("FILE_HASH_MISMATCH", "压缩包摘要与授权不一致")
	}

	reader, err := zip.NewReader(upload, written)
	if err != nil {
		upload.Close()
		return ImportResult{}, apperr.Wrap("INVALID_ARCHIVE", "压缩包格式无效", err)
	}
	entries, result, err := s.validateArchive(reader)
	if err != nil {
		upload.Close()
		return ImportResult{}, err
	}

	stage, err := os.MkdirTemp(parent, "."+filepath.Base(root)+".prism-import-*")
	if err != nil {
		upload.Close()
		return ImportResult{}, apperr.Wrap("FILE_WRITE_FAILED", "无法创建解压临时目录", err)
	}
	defer os.RemoveAll(stage)
	if err = extractArchive(stage, entries, s.maxExtract); err != nil {
		upload.Close()
		return ImportResult{}, err
	}
	if err = upload.Close(); err != nil {
		return ImportResult{}, apperr.Wrap("FILE_WRITE_FAILED", "压缩包临时文件关闭失败", err)
	}
	if info, statErr := os.Stat(root); statErr == nil {
		if chmodErr := os.Chmod(stage, info.Mode().Perm()); chmodErr != nil {
			return ImportResult{}, apperr.Wrap("FILE_WRITE_FAILED", "无法设置工作目录权限", chmodErr)
		}
	} else {
		return ImportResult{}, fileError(statErr, "工作目录不可用")
	}
	if err = publishImportedDirectory(root, stage); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func (s *Service) validateArchive(reader *zip.Reader) ([]archiveEntry, ImportResult, error) {
	return s.validateArchiveEncoding(reader, "utf-8")
}

func (s *Service) validateArchiveEncoding(reader *zip.Reader, encoding string) ([]archiveEntry, ImportResult, error) {
	if len(reader.File) > maxArchiveEntries {
		return nil, ImportResult{}, apperr.New("INVALID_ARCHIVE", "压缩包条目数量超过限制")
	}
	entries := make([]archiveEntry, 0, len(reader.File))
	seen := make(map[string]struct{}, len(reader.File))
	result := ImportResult{}
	for _, file := range reader.File {
		name, err := decodeArchiveEntryName(file.Name, encoding)
		if err != nil {
			return nil, ImportResult{}, err
		}
		clean, err := normalizeArchiveEntry(name)
		if err != nil {
			return nil, ImportResult{}, err
		}
		mode := file.Mode()
		isDirectory := file.FileInfo().IsDir()
		if clean == "." && isDirectory {
			continue
		}
		if clean == "." || mode&os.ModeSymlink != 0 || (!isDirectory && !mode.IsRegular()) {
			return nil, ImportResult{}, apperr.New("INVALID_ARCHIVE", "压缩包包含不支持的文件类型")
		}
		key := clean
		if filepath.Separator == '\\' {
			key = strings.ToLower(key)
		}
		if _, exists := seen[key]; exists {
			return nil, ImportResult{}, apperr.New("INVALID_ARCHIVE", "压缩包包含重复路径")
		}
		seen[key] = struct{}{}
		if isDirectory {
			result.Directories++
		} else {
			if file.UncompressedSize64 > uint64(s.maxExtract-result.Bytes) {
				return nil, ImportResult{}, apperr.New("FILE_TOO_LARGE", "压缩包解压后超过节点限制")
			}
			result.Files++
			result.Bytes += int64(file.UncompressedSize64)
		}
		entries = append(entries, archiveEntry{file: file, path: clean})
	}
	return entries, result, nil
}

func decodeArchiveEntryName(name, encoding string) (string, error) {
	switch encoding {
	case "utf-8":
		if !utf8.ValidString(name) {
			return "", apperr.New("INVALID_ARCHIVE", "压缩包包含非 UTF-8 文件名，请选择正确的解压编码")
		}
		return name, nil
	case "gbk":
		decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes([]byte(name))
		if err != nil || !utf8.Valid(decoded) {
			return "", apperr.New("INVALID_ARCHIVE", "压缩包文件名无法按 GBK 解码")
		}
		return string(decoded), nil
	default:
		return "", apperr.New("INVALID_REQUEST", "解压编码无效")
	}
}

func normalizeArchiveEntry(name string) (string, error) {
	if name == "" || !utf8.ValidString(name) {
		return "", apperr.New("INVALID_ARCHIVE", "压缩包包含无效文件名")
	}
	clean, err := normalizeRelative(name)
	if err != nil {
		return "", apperr.New("INVALID_ARCHIVE", "压缩包文件路径越出工作目录")
	}
	return clean, nil
}

func extractArchive(stage string, entries []archiveEntry, maxBytes int64) error {
	var extracted int64
	for _, entry := range entries {
		target := filepath.Join(stage, filepath.FromSlash(entry.path))
		if !pathWithin(stage, target) {
			return apperr.New("INVALID_ARCHIVE", "压缩包文件路径越出工作目录")
		}
		if entry.file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return apperr.Wrap("FILE_WRITE_FAILED", "压缩包目录创建失败", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "压缩包目录创建失败", err)
		}
		input, err := entry.file.Open()
		if err != nil {
			return apperr.Wrap("INVALID_ARCHIVE", "压缩包条目读取失败", err)
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			input.Close()
			return apperr.Wrap("INVALID_ARCHIVE", "压缩包文件路径冲突", err)
		}
		remaining := maxBytes - extracted
		written, copyErr := io.Copy(output, io.LimitReader(input, remaining+1))
		syncErr := output.Sync()
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		if copyErr != nil || syncErr != nil || closeOutputErr != nil || closeInputErr != nil {
			return apperr.Wrap("INVALID_ARCHIVE", "压缩包条目解压失败", errors.Join(copyErr, syncErr, closeOutputErr, closeInputErr))
		}
		if written > remaining || uint64(written) != entry.file.UncompressedSize64 {
			return apperr.New("FILE_TOO_LARGE", "压缩包实际解压大小超过限制")
		}
		permission := entry.file.Mode().Perm()
		if permission == 0 {
			permission = 0o640
		}
		if err := os.Chmod(target, permission); err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "压缩包文件权限设置失败", err)
		}
		extracted += written
	}
	return nil
}

func publishImportedDirectory(root, stage string) error {
	empty, err := directoryEmpty(root)
	if err != nil {
		return err
	}
	if !empty {
		return apperr.New("DIRECTORY_NOT_EMPTY", "导入期间工作目录已产生文件，请重试")
	}
	backup := stage + ".previous"
	if err := os.Rename(root, backup); err != nil {
		return apperr.Wrap("FILE_WRITE_FAILED", "工作目录切换失败", err)
	}
	backupEmpty, checkErr := directoryEmpty(backup)
	if checkErr != nil || !backupEmpty {
		rollbackErr := os.Rename(backup, root)
		if checkErr == nil {
			checkErr = apperr.New("DIRECTORY_NOT_EMPTY", "导入期间工作目录已产生文件，请重试")
		}
		return apperr.Wrap("FILE_WRITE_FAILED", "工作目录状态发生变化", errors.Join(checkErr, rollbackErr))
	}
	if err := os.Rename(stage, root); err != nil {
		rollbackErr := error(nil)
		if _, statErr := os.Lstat(root); errors.Is(statErr, os.ErrNotExist) {
			rollbackErr = os.Rename(backup, root)
		} else {
			rollbackErr = os.Remove(backup)
		}
		return apperr.Wrap("FILE_WRITE_FAILED", "解压目录发布失败", errors.Join(err, rollbackErr))
	}
	_ = os.Remove(backup)
	return nil
}

func directoryEmpty(directory string) (bool, error) {
	file, err := os.Open(directory)
	if err != nil {
		return false, fileError(err, "工作目录不可用")
	}
	defer file.Close()
	_, err = file.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return false, fileError(err, "工作目录读取失败")
	}
	return false, nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
