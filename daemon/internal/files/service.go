package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/atomicfile"
	"PrismPanel-daemon/internal/deployment"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/supervisor"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

const (
	maxDirectoriesPerList = 32
	maxEntriesPerList     = 5000
)

type Target struct {
	Type string `json:"resource_type"`
	ID   string `json:"resource_id"`
}

type DirectoryRequest struct {
	Path   string `json:"path"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type Entry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	Mode       uint32    `json:"mode"`
}

type DirectoryResult struct {
	Path       string        `json:"path"`
	Entries    []Entry       `json:"entries"`
	NextCursor string        `json:"next_cursor"`
	Truncated  bool          `json:"truncated"`
	Error      *apperr.Error `json:"error"`
}

type Content struct {
	Path       string    `json:"path"`
	Content    string    `json:"content"`
	Encoding   string    `json:"encoding"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	Version    string    `json:"version"`
}

type Service struct {
	servers     *serverservice.Service
	supervisor  *supervisor.Manager
	deployments *deployment.Manager
	maxEdit     int64
	maxUpload   int64
	maxExtract  int64
	transfers   chan struct{}
}

func NewService(servers *serverservice.Service, processManager *supervisor.Manager, deployments *deployment.Manager, maxEdit, maxUpload, maxExtract int64, concurrentTransfers int) *Service {
	return &Service{
		servers: servers, supervisor: processManager, deployments: deployments,
		maxEdit: maxEdit, maxUpload: maxUpload, maxExtract: maxExtract,
		transfers: make(chan struct{}, concurrentTransfers),
	}
}

func (s *Service) List(target Target, directories []DirectoryRequest, includeHidden bool) ([]DirectoryResult, error) {
	root, err := s.root(target)
	if err != nil {
		return nil, err
	}
	if len(directories) == 0 || len(directories) > maxDirectoriesPerList {
		return nil, apperr.New("INVALID_REQUEST", "目录请求数量必须在 1 到 32 之间")
	}
	results := make([]DirectoryResult, 0, len(directories))
	total := 0
	for _, request := range directories {
		result := s.listDirectory(root, request, includeHidden, maxEntriesPerList-total)
		total += len(result.Entries)
		results = append(results, result)
	}
	return results, nil
}

func (s *Service) listDirectory(root string, request DirectoryRequest, includeHidden bool, remaining int) DirectoryResult {
	relative, err := normalizeRelative(request.Path)
	result := DirectoryResult{Path: relative, Entries: []Entry{}}
	if err != nil {
		result.Error = apperr.From(err)
		return result
	}
	target, err := securePath(root, relative, false)
	if err != nil {
		result.Error = apperr.From(err)
		return result
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		result.Error = fileError(err, "目录读取失败")
		return result
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	offset, err := decodeCursor(request.Cursor)
	if err != nil {
		result.Error = apperr.From(err)
		return result
	}
	limit := request.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if limit > remaining {
		limit = remaining
	}
	visible := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !includeHidden && strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		visible = append(visible, entry)
	}
	if offset > len(visible) {
		offset = len(visible)
	}
	end := offset + limit
	if end > len(visible) {
		end = len(visible)
	}
	for _, entry := range visible[offset:end] {
		info, infoErr := entry.Info()
		if infoErr != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		entryType := "file"
		if entry.IsDir() {
			entryType = "directory"
		} else if !info.Mode().IsRegular() {
			continue
		}
		entryPath := path.Join(relative, entry.Name())
		if relative == "." {
			entryPath = entry.Name()
		}
		result.Entries = append(result.Entries, Entry{
			Name: entry.Name(), Path: entryPath, Type: entryType, Size: info.Size(),
			ModifiedAt: info.ModTime().UTC(), Mode: uint32(info.Mode().Perm()),
		})
	}
	if end < len(visible) {
		result.Truncated = true
		result.NextCursor = encodeCursor(end)
	}
	return result
}

func (s *Service) Read(target Target, relative string) (Content, error) {
	root, err := s.root(target)
	if err != nil {
		return Content{}, err
	}
	clean, err := normalizeRelative(relative)
	if err != nil || clean == "." {
		return Content{}, apperr.New("INVALID_REQUEST", "必须指定文件路径")
	}
	filePath, err := securePath(root, clean, false)
	if err != nil {
		return Content{}, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return Content{}, fileError(err, "文件读取失败")
	}
	if !info.Mode().IsRegular() {
		return Content{}, apperr.New("INVALID_REQUEST", "目标不是普通文件")
	}
	if info.Size() > s.maxEdit {
		return Content{}, apperr.New("FILE_TOO_LARGE", "文件超过在线编辑大小限制")
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return Content{}, fileError(err, "文件读取失败")
	}
	text, encoding, err := decodeText(raw)
	if err != nil {
		return Content{}, err
	}
	return Content{
		Path: clean, Content: text, Encoding: encoding, Size: int64(len(raw)),
		ModifiedAt: info.ModTime().UTC(), Version: version(raw),
	}, nil
}

func (s *Service) Save(target Target, relative, content, encoding, expectedVersion string) (Content, error) {
	var result Content
	err := s.mutate(target, func(root string) error {
		clean, err := normalizeRelative(relative)
		if err != nil || clean == "." {
			return apperr.New("INVALID_REQUEST", "必须指定文件路径")
		}
		filePath, err := securePath(root, clean, false)
		if err != nil {
			return err
		}
		info, err := os.Stat(filePath)
		if err != nil {
			return fileError(err, "文件读取失败")
		}
		if !info.Mode().IsRegular() {
			return apperr.New("INVALID_REQUEST", "目标不是普通文件")
		}
		current, err := os.ReadFile(filePath)
		if err != nil {
			return fileError(err, "文件读取失败")
		}
		if expectedVersion == "" || version(current) != expectedVersion {
			return apperr.New("FILE_CHANGED", "文件已被其他程序修改")
		}
		raw, normalizedEncoding, err := encodeText(content, encoding)
		if err != nil {
			return err
		}
		if int64(len(raw)) > s.maxEdit {
			return apperr.New("FILE_TOO_LARGE", "文件超过在线编辑大小限制")
		}
		if err := atomicfile.WriteFile(filePath, raw, info.Mode().Perm()); err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "文件保存失败", err)
		}
		updated, _ := os.Stat(filePath)
		result = Content{Path: clean, Content: content, Encoding: normalizedEncoding, Size: int64(len(raw)), Version: version(raw)}
		if updated != nil {
			result.ModifiedAt = updated.ModTime().UTC()
		}
		return nil
	})
	return result, err
}

func (s *Service) Create(target Target, relative, kind string) error {
	return s.mutate(target, func(root string) error {
		clean, err := normalizeRelative(relative)
		if err != nil || clean == "." {
			return apperr.New("INVALID_REQUEST", "必须指定新文件或目录路径")
		}
		targetPath, err := securePath(root, clean, true)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(targetPath); err == nil {
			if kind == "directory" && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return nil
			}
			return apperr.New("FILE_EXISTS", "目标已存在")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fileError(err, "无法检查目标路径")
		}
		switch kind {
		case "file":
			file, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
			if err == nil {
				err = file.Close()
			}
			if err != nil {
				return fileError(err, "文件创建失败")
			}
		case "directory":
			if err := os.Mkdir(targetPath, 0o750); err != nil {
				if errors.Is(err, os.ErrExist) {
					info, statErr := os.Lstat(targetPath)
					if statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
						return nil
					}
				}
				return fileError(err, "目录创建失败")
			}
		default:
			return apperr.New("INVALID_REQUEST", "创建类型必须是 file 或 directory")
		}
		return nil
	})
}

func (s *Service) Upload(target Target, relative string, source io.Reader, expectedSize int64, expectedSHA string, overwrite bool) (Entry, error) {
	if expectedSize < 0 || expectedSize > s.maxUpload {
		return Entry{}, apperr.New("FILE_TOO_LARGE", "上传文件超过节点限制")
	}
	if !s.acquireTransfer() {
		return Entry{}, apperr.New("TOO_MANY_REQUESTS", "文件传输并发数已达到上限")
	}
	defer s.releaseTransfer()
	var result Entry
	err := s.mutate(target, func(root string) error {
		clean, err := normalizeRelative(relative)
		if err != nil || clean == "." {
			return apperr.New("INVALID_REQUEST", "必须指定上传目标路径")
		}
		targetPath, err := securePath(root, clean, true)
		if err != nil {
			return err
		}
		if info, statErr := os.Lstat(targetPath); statErr == nil {
			if info.IsDir() || !info.Mode().IsRegular() {
				return apperr.New("INVALID_REQUEST", "上传目标不是普通文件")
			}
			if !overwrite {
				return apperr.New("FILE_EXISTS", "目标文件已存在")
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fileError(statErr, "无法检查上传目标")
		}
		temp, err := os.CreateTemp(filepath.Dir(targetPath), ".prism-upload-*")
		if err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "无法创建上传临时文件", err)
		}
		tempPath := temp.Name()
		defer os.Remove(tempPath)
		if err := temp.Chmod(0o640); err != nil {
			temp.Close()
			return apperr.Wrap("FILE_WRITE_FAILED", "无法设置上传文件权限", err)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(source, s.maxUpload+1))
		syncErr := temp.Sync()
		closeErr := temp.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "上传文件写入失败", errors.Join(copyErr, syncErr, closeErr))
		}
		if written > s.maxUpload || written != expectedSize {
			return apperr.New("FILE_SIZE_MISMATCH", "上传文件大小与授权不一致")
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		if expectedSHA != "" && !strings.EqualFold(expectedSHA, digest) {
			return apperr.New("FILE_HASH_MISMATCH", "上传文件摘要与授权不一致")
		}
		if err := atomicfile.Publish(tempPath, targetPath, overwrite); err != nil {
			if errors.Is(err, os.ErrExist) {
				return apperr.New("FILE_EXISTS", "目标文件已存在")
			}
			return apperr.Wrap("FILE_WRITE_FAILED", "上传文件发布失败", err)
		}
		info, err := os.Stat(targetPath)
		if err != nil {
			return fileError(err, "无法读取上传结果")
		}
		result = Entry{Name: path.Base(clean), Path: clean, Type: "file", Size: info.Size(), ModifiedAt: info.ModTime().UTC(), Mode: uint32(info.Mode().Perm())}
		return nil
	})
	return result, err
}

type Download struct {
	File    *os.File
	Name    string
	Size    int64
	Mode    time.Time
	cleanup func()
	release func()
}

func (d *Download) Close() error {
	err := d.File.Close()
	if d.cleanup != nil {
		d.cleanup()
		d.cleanup = nil
	}
	if d.release != nil {
		d.release()
		d.release = nil
	}
	return err
}

func (s *Service) OpenDownload(target Target, relative string) (*Download, error) {
	if !s.acquireTransfer() {
		return nil, apperr.New("TOO_MANY_REQUESTS", "文件传输并发数已达到上限")
	}
	root, err := s.root(target)
	if err != nil {
		s.releaseTransfer()
		return nil, err
	}
	clean, err := normalizeRelative(relative)
	if err != nil || clean == "." {
		s.releaseTransfer()
		return nil, apperr.New("INVALID_REQUEST", "必须指定下载文件或目录路径")
	}
	filePath, err := securePath(root, clean, false)
	if err != nil {
		s.releaseTransfer()
		return nil, err
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		s.releaseTransfer()
		return nil, fileError(err, "下载目标读取失败")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		s.releaseTransfer()
		return nil, apperr.New("PATH_ESCAPE", "下载目标不能是符号链接")
	}
	if info.IsDir() {
		download, archiveErr := createDirectoryDownload(filePath, path.Base(clean))
		if archiveErr != nil {
			s.releaseTransfer()
			return nil, archiveErr
		}
		download.release = s.releaseTransfer
		return download, nil
	}
	if !info.Mode().IsRegular() {
		s.releaseTransfer()
		return nil, apperr.New("INVALID_REQUEST", "下载目标不是普通文件或目录")
	}
	file, err := os.Open(filePath)
	if err != nil {
		s.releaseTransfer()
		return nil, fileError(err, "文件打开失败")
	}
	return &Download{File: file, Name: path.Base(clean), Size: info.Size(), Mode: info.ModTime().UTC(), release: s.releaseTransfer}, nil
}

func (s *Service) Move(target Target, source, destination string, overwrite bool) error {
	return s.mutate(target, func(root string) error {
		sourceRelative, err := normalizeRelative(source)
		if err != nil || sourceRelative == "." {
			return apperr.New("INVALID_REQUEST", "必须指定移动源路径")
		}
		destinationRelative, err := normalizeRelative(destination)
		if err != nil || destinationRelative == "." {
			return apperr.New("INVALID_REQUEST", "必须指定移动目标路径")
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
			if !overwrite {
				return apperr.New("FILE_EXISTS", "移动目标已存在")
			}
			return apperr.New("INVALID_REQUEST", "首版移动操作不允许覆盖现有目标")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fileError(err, "无法检查移动目标")
		}
		if err := os.Rename(sourcePath, destinationPath); err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "文件移动失败", err)
		}
		return nil
	})
}

func (s *Service) Delete(target Target, paths []string, recursive bool) error {
	if len(paths) == 0 || len(paths) > 100 {
		return apperr.New("INVALID_REQUEST", "删除目标数量必须在 1 到 100 之间")
	}
	return s.mutate(target, func(root string) error {
		for _, relative := range paths {
			clean, err := normalizeRelative(relative)
			if err != nil || clean == "." {
				return apperr.New("INVALID_REQUEST", "不能删除工作目录根路径")
			}
			targetPath, err := securePath(root, clean, false)
			if err != nil {
				return err
			}
			info, err := os.Lstat(targetPath)
			if err != nil {
				return fileError(err, "删除目标读取失败")
			}
			if info.IsDir() {
				if !recursive {
					return apperr.New("RECURSIVE_REQUIRED", "删除目录必须显式允许递归")
				}
				if err := os.RemoveAll(targetPath); err != nil {
					return apperr.Wrap("FILE_WRITE_FAILED", "目录删除失败", err)
				}
			} else if err := os.Remove(targetPath); err != nil {
				return apperr.Wrap("FILE_WRITE_FAILED", "文件删除失败", err)
			}
		}
		return nil
	})
}

func (s *Service) root(target Target) (string, error) {
	switch target.Type {
	case "instance":
		snapshot, err := s.supervisor.Get(target.ID)
		if err != nil {
			return "", err
		}
		return filepath.Clean(snapshot.Workspace), nil
	case "image":
		server, err := s.servers.Get(target.ID)
		if err != nil {
			return "", err
		}
		if server.Type != "mirror" {
			return "", apperr.New("INVALID_STATE", "目标服务器没有镜像源")
		}
		return filepath.Join(server.RootPath, server.ImageDirectory), nil
	default:
		return "", apperr.New("INVALID_REQUEST", "文件资源类型无效")
	}
}

func (s *Service) mutate(target Target, operation func(root string) error) error {
	return s.mutateWithState(target, false, operation)
}

func (s *Service) mutateStopped(target Target, operation func(root string) error) error {
	return s.mutateWithState(target, true, operation)
}

func (s *Service) mutateWithState(target Target, requireStopped bool, operation func(root string) error) error {
	switch target.Type {
	case "instance":
		mutate := s.supervisor.WithFileMutation
		if requireStopped {
			mutate = s.supervisor.WithStoppedFileMutation
		}
		return mutate(target.ID, func(workspace string) error {
			root, err := ensureAbsoluteDirectory(workspace)
			if err != nil {
				return err
			}
			return operation(root)
		})
	case "image":
		server, err := s.servers.Get(target.ID)
		if err != nil {
			return err
		}
		if server.Type != "mirror" {
			return apperr.New("INVALID_STATE", "目标服务器没有镜像源")
		}
		return s.deployments.WithImageMutation(target.ID, func() error {
			root, err := ensureConfiguredDirectory(server.RootPath, server.ImageDirectory)
			if err != nil {
				return err
			}
			return operation(root)
		})
	default:
		return apperr.New("INVALID_REQUEST", "文件资源类型无效")
	}
}

func (s *Service) acquireTransfer() bool {
	select {
	case s.transfers <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) releaseTransfer() { <-s.transfers }

func normalizeRelative(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" {
		value = "."
	}
	if strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") ||
		filepath.IsAbs(filepath.FromSlash(value)) || filepath.VolumeName(filepath.FromSlash(value)) != "" {
		return "", apperr.New("PATH_ESCAPE", "文件路径无效")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", apperr.New("PATH_ESCAPE", "文件路径越出工作目录")
		}
	}
	clean := path.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", apperr.New("PATH_ESCAPE", "文件路径越出工作目录")
	}
	return clean, nil
}

func securePath(root, relative string, allowMissingFinal bool) (string, error) {
	clean, err := normalizeRelative(relative)
	if err != nil {
		return "", err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", apperr.Wrap("INVALID_CONFIG", "无法解析工作目录", err)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return "", fileError(err, "工作目录不可用")
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", apperr.New("PATH_ESCAPE", "工作目录不能是符号链接且必须是目录")
	}
	if clean == "." {
		return absoluteRoot, nil
	}
	parts := strings.Split(clean, "/")
	current := absoluteRoot
	for index, part := range parts {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, statErr := os.Lstat(current)
		last := index == len(parts)-1
		if errors.Is(statErr, os.ErrNotExist) && last && allowMissingFinal {
			break
		}
		if statErr != nil {
			return "", fileError(statErr, "文件路径不可用")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", apperr.New("PATH_ESCAPE", "文件路径不能经过符号链接")
		}
		if !last && !info.IsDir() {
			return "", apperr.New("INVALID_REQUEST", "文件路径的父级不是目录")
		}
	}
	rel, err := filepath.Rel(absoluteRoot, current)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", apperr.New("PATH_ESCAPE", "文件路径越出工作目录")
	}
	return current, nil
}

func ensureConfiguredDirectory(base, relative string) (string, error) {
	absoluteBase, err := filepath.Abs(base)
	if err != nil {
		return "", apperr.Wrap("INVALID_CONFIG", "无法解析镜像根目录", err)
	}
	baseInfo, err := os.Lstat(absoluteBase)
	if errors.Is(err, os.ErrNotExist) {
		absoluteBase, err = ensureAbsoluteDirectory(absoluteBase)
		if err == nil {
			baseInfo, err = os.Lstat(absoluteBase)
		}
	}
	if err != nil {
		return "", fileError(err, "镜像根目录不可用")
	}
	if baseInfo.Mode()&os.ModeSymlink != 0 || !baseInfo.IsDir() {
		return "", apperr.New("PATH_ESCAPE", "镜像根目录不能是符号链接且必须是目录")
	}
	clean, err := normalizeRelative(relative)
	if err != nil || clean == "." {
		return "", apperr.New("INVALID_CONFIG", "镜像目录配置无效")
	}
	current := absoluteBase
	for _, part := range strings.Split(clean, "/") {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o750); err != nil {
				return "", apperr.Wrap("FILE_WRITE_FAILED", "镜像目录创建失败", err)
			}
			continue
		}
		if statErr != nil {
			return "", fileError(statErr, "镜像目录不可用")
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", apperr.New("PATH_ESCAPE", "镜像目录不能经过符号链接")
		}
	}
	return current, nil
}

func ensureAbsoluteDirectory(target string) (string, error) {
	absolute, err := filepath.Abs(target)
	if err != nil {
		return "", apperr.Wrap("INVALID_CONFIG", "无法解析实例工作目录", err)
	}
	missing := make([]string, 0)
	current := absolute
	for {
		info, statErr := os.Lstat(current)
		if statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", apperr.New("PATH_ESCAPE", "实例工作目录不能经过符号链接")
			}
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil || !sameFilePath(current, resolved) {
				return "", apperr.New("PATH_ESCAPE", "实例工作目录不能经过符号链接")
			}
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", fileError(statErr, "实例工作目录不可用")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", apperr.New("INVALID_CONFIG", "实例工作目录没有可用父目录")
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	for index := len(missing) - 1; index >= 0; index-- {
		current = filepath.Join(current, missing[index])
		if err := os.Mkdir(current, 0o750); err != nil {
			return "", apperr.Wrap("FILE_WRITE_FAILED", "实例工作目录创建失败", err)
		}
	}
	return absolute, nil
}

func sameFilePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if filepath.Separator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func decodeText(raw []byte) (string, string, error) {
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", "", apperr.New("UNSUPPORTED_ENCODING", "二进制文件不能在线编辑")
	}
	if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		content := raw[3:]
		if !utf8.Valid(content) {
			return "", "", apperr.New("UNSUPPORTED_ENCODING", "文件编码无法识别")
		}
		return string(content), "utf-8-bom", nil
	}
	if utf8.Valid(raw) {
		return string(raw), "utf-8", nil
	}
	decoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), raw)
	if err != nil || !utf8.Valid(decoded) {
		return "", "", apperr.New("UNSUPPORTED_ENCODING", "文件编码不是 UTF-8 或 GBK")
	}
	return string(decoded), "gbk", nil
}

func encodeText(content, encoding string) ([]byte, string, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "utf8":
		return []byte(content), "utf-8", nil
	case "utf-8-bom":
		return append([]byte{0xef, 0xbb, 0xbf}, []byte(content)...), "utf-8-bom", nil
	case "gbk":
		encoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(content))
		if err != nil {
			return nil, "", apperr.Wrap("UNSUPPORTED_ENCODING", "内容无法使用 GBK 编码", err)
		}
		return encoded, "gbk", nil
	default:
		return nil, "", apperr.New("UNSUPPORTED_ENCODING", "仅支持 UTF-8 和 GBK 编码")
	}
}

func version(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, apperr.New("INVALID_CURSOR", "目录游标无效")
	}
	offset, err := strconv.Atoi(string(raw))
	if err != nil || offset < 0 {
		return 0, apperr.New("INVALID_CURSOR", "目录游标无效")
	}
	return offset, nil
}

func fileError(err error, message string) *apperr.Error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return apperr.New("FILE_NOT_FOUND", "文件或目录不存在")
	case errors.Is(err, os.ErrPermission):
		return apperr.New("PERMISSION_DENIED", "节点进程无权访问文件或目录")
	default:
		return apperr.Wrap("FILE_OPERATION_FAILED", message, err)
	}
}
