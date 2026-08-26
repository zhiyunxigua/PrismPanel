package fileopen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const MaxOpenFileSize int64 = 64 * 1024 * 1024

type Runtime struct {
	APIBaseURL   string
	ProxySession string
}

type Input struct {
	NodeID       string `json:"node_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
}

type OpenedFile struct {
	Path     string `json:"path"`
	Watching bool   `json:"watching"`
}

type Event struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	Message string `json:"message,omitempty"`
}

type emitter func(Event)

type Service struct {
	cacheDir string
	emit     emitter
	client   *http.Client

	mu    sync.Mutex
	ctx   context.Context
	tasks map[string]*watchTask
}

type watchTask struct {
	input          Input
	runtime        Runtime
	localPath      string
	baseVersion    string
	lastSize       int64
	lastModTime    time.Time
	remoteModified string
	remoteSize     int64
	remoteETag     string
	stableSince    time.Time
	syncing        bool
	polling        bool
	conflicted     bool
}

type grant struct {
	Mode         string `json:"mode"`
	Endpoint     string `json:"endpoint"`
	Ticket       string `json:"ticket"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Path         string `json:"path"`
	ChunkSize    int64  `json:"chunk_size"`
}

func New(cacheDir string, emit emitter) *Service {
	return &Service{
		cacheDir: cacheDir,
		emit:     emit,
		client:   &http.Client{Timeout: 30 * time.Minute},
		tasks:    make(map[string]*watchTask),
	}
}

func DefaultCacheDir() (string, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve WinApp cache directory: %w", err)
	}
	return filepath.Join(directory, "PrismPanel", "opened-files"), nil
}

func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

func (s *Service) Open(ctx context.Context, runtime Runtime, input Input, chooseApplication bool) (OpenedFile, error) {
	if err := validateInput(runtime, input); err != nil {
		return OpenedFile{}, err
	}
	localPath, err := s.localPath(input)
	if err != nil {
		return OpenedFile{}, err
	}

	s.mu.Lock()
	existing := s.tasks[localPath]
	s.mu.Unlock()
	if existing == nil {
		version, info, remoteModified, remoteSize, err := s.download(ctx, runtime, input, localPath)
		if err != nil {
			return OpenedFile{}, err
		}
		remoteETag := s.fetchRemoteETag(ctx, runtime, input)
		task := &watchTask{
			input: input, runtime: runtime, localPath: localPath, baseVersion: version,
			lastSize: info.Size(), lastModTime: info.ModTime(), remoteModified: remoteModified, remoteSize: remoteSize, remoteETag: remoteETag,
		}
		s.mu.Lock()
		s.tasks[localPath] = task
		serviceContext := s.ctx
		s.mu.Unlock()
		if serviceContext == nil {
			serviceContext = context.Background()
		}
		go s.watch(serviceContext, task)
	}
	if err := launchFile(localPath, chooseApplication); err != nil {
		return OpenedFile{}, err
	}
	return OpenedFile{Path: localPath, Watching: true}, nil
}

func validateInput(runtime Runtime, input Input) error {
	if runtime.APIBaseURL == "" || runtime.ProxySession == "" {
		return errors.New("WinApp 尚未连接 Panel")
	}
	if input.NodeID == "" || input.ResourceID == "" || input.Path == "" || input.Name == "" ||
		(input.ResourceType != "instance" && input.ResourceType != "image") {
		return errors.New("本机打开参数无效")
	}
	if input.Size < 0 || input.Size > MaxOpenFileSize {
		return fmt.Errorf("文件超过本机打开限制（%d MiB）", MaxOpenFileSize/1024/1024)
	}
	return nil
}

func (s *Service) localPath(input Input) (string, error) {
	identity := strings.Join([]string{input.NodeID, input.ResourceType, input.ResourceID, input.Path}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	directory := filepath.Join(s.cacheDir, hex.EncodeToString(digest[:12]))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create opened file cache: %w", err)
	}
	return filepath.Join(directory, safeFileName(input.Name)), nil
}

func safeFileName(value string) string {
	name := filepath.Base(strings.TrimSpace(value))
	name = strings.Map(func(char rune) rune {
		if strings.ContainsRune(`<>:"/\\|?*`, char) || char < 32 {
			return '_'
		}
		return char
	}, name)
	name = strings.TrimRight(name, ". ")
	if name == "" || name == "." {
		return "remote-file"
	}
	return name
}

func (s *Service) download(ctx context.Context, runtime Runtime, input Input, destination string) (string, os.FileInfo, string, int64, error) {
	target, err := buildExportURL(runtime, input)
	if err != nil {
		return "", nil, "", 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", nil, "", 0, err
	}
	request.Header.Set("X-Prism-Client-Session", runtime.ProxySession)
	response, err := s.client.Do(request)
	if err != nil {
		return "", nil, "", 0, fmt.Errorf("下载远程文件: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", nil, "", 0, decodeResponseError(response)
	}
	if response.ContentLength > MaxOpenFileSize {
		return "", nil, "", 0, fmt.Errorf("文件超过本机打开限制（%d MiB）", MaxOpenFileSize/1024/1024)
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".prism-open-*")
	if err != nil {
		return "", nil, "", 0, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, MaxOpenFileSize+1))
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return "", nil, "", 0, fmt.Errorf("写入本机缓存: %w", err)
	}
	if written > MaxOpenFileSize {
		return "", nil, "", 0, fmt.Errorf("文件超过本机打开限制（%d MiB）", MaxOpenFileSize/1024/1024)
	}
	if err := replaceLocalFile(tempPath, destination); err != nil {
		return "", nil, "", 0, fmt.Errorf("发布本机缓存: %w", err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		return "", nil, "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), info, response.Header.Get("Last-Modified"), info.Size(), nil
}

func replaceLocalFile(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, destination)
}

func (s *Service) pollRemote(task *watchTask) {
	s.mu.Lock()
	if task.syncing || task.polling || task.conflicted {
		s.mu.Unlock()
		return
	}
	task.polling = true
	runtime, input := task.runtime, task.input
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		task.polling = false
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	target, err := buildExportURL(runtime, input)
	if err != nil {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return
	}
	request.Header.Set("X-Prism-Client-Session", runtime.ProxySession)
	request.Header.Set("Range", "bytes=0-0")
	response, err := s.client.Do(request)
	if err != nil {
		return
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}
	remoteModified := response.Header.Get("Last-Modified")
	remoteSize := contentRangeSize(response.Header.Get("Content-Range"), response.ContentLength)
	remoteETag := response.Header.Get("ETag")

	s.mu.Lock()
	changed := (remoteModified != "" && task.remoteModified != "" && remoteModified != task.remoteModified) ||
		(remoteSize > 0 && task.remoteSize > 0 && remoteSize != task.remoteSize) ||
		(remoteETag != "" && task.remoteETag != "" && remoteETag != task.remoteETag)
	if !changed {
		if task.remoteModified == "" {
			task.remoteModified = remoteModified
		}
		if task.remoteSize == 0 {
			task.remoteSize = remoteSize
		}
		if task.remoteETag == "" {
			task.remoteETag = remoteETag
		}
		s.mu.Unlock()
		return
	}
	info, statErr := os.Stat(task.localPath)
	localChanged := statErr != nil || info.Size() != task.lastSize || !info.ModTime().Equal(task.lastModTime)
	if localChanged {
		task.conflicted = true
		s.mu.Unlock()
		s.emitEvent(Event{Type: "error", Path: input.Path, Message: "云端文件已变化，本地副本也有修改，已停止自动回传"})
		return
	}
	task.syncing = true
	s.mu.Unlock()

	version, updated, updatedModified, updatedSize, downloadErr := s.download(ctx, runtime, input, task.localPath)
	s.mu.Lock()
	task.syncing = false
	if downloadErr == nil {
		task.baseVersion = version
		task.lastSize = updated.Size()
		task.lastModTime = updated.ModTime()
		task.remoteModified = updatedModified
		task.remoteSize = updatedSize
		task.remoteETag = remoteETag
	}
	s.mu.Unlock()
	if downloadErr != nil {
		s.emitEvent(Event{Type: "error", Path: input.Path, Message: "云端文件重新下载失败: " + downloadErr.Error()})
		return
	}
	s.emitEvent(Event{Type: "updated", Path: input.Path})
}

func (s *Service) fetchRemoteETag(ctx context.Context, runtime Runtime, input Input) string {
	target, err := buildExportURL(runtime, input)
	if err != nil {
		return ""
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ""
	}
	request.Header.Set("X-Prism-Client-Session", runtime.ProxySession)
	request.Header.Set("Range", "bytes=0-0")
	response, err := s.client.Do(request)
	if err != nil {
		return ""
	}
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ""
	}
	return response.Header.Get("ETag")
}

func buildExportURL(runtime Runtime, input Input) (string, error) {
	target, err := url.Parse(strings.TrimRight(runtime.APIBaseURL, "/") + "/api/v1/files/export")
	if err != nil {
		return "", err
	}
	query := target.Query()
	query.Set("node_id", input.NodeID)
	query.Set("resource_type", input.ResourceType)
	query.Set("resource_id", input.ResourceID)
	query.Set("path", input.Path)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func contentRangeSize(value string, fallback int64) int64 {
	parts := strings.Split(value, "/")
	if len(parts) == 2 {
		var size int64
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &size); err == nil {
			return size
		}
	}
	return fallback
}

func (s *Service) watch(ctx context.Context, task *watchTask) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.inspect(task)
			if time.Now().Unix()%15 == 0 {
				go s.pollRemote(task)
			}
		}
	}
}

func (s *Service) inspect(task *watchTask) {
	s.mu.Lock()
	if task.syncing || task.conflicted {
		s.mu.Unlock()
		return
	}
	info, err := os.Stat(task.localPath)
	if err != nil {
		s.mu.Unlock()
		return
	}
	changed := info.Size() != task.lastSize || !info.ModTime().Equal(task.lastModTime)
	if !changed {
		task.stableSince = time.Time{}
		s.mu.Unlock()
		return
	}
	if task.stableSince.IsZero() {
		task.stableSince = time.Now()
		s.mu.Unlock()
		return
	}
	if time.Since(task.stableSince) < time.Second {
		s.mu.Unlock()
		return
	}
	task.syncing = true
	s.mu.Unlock()
	go s.sync(task)
}

func (s *Service) sync(task *watchTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	err := s.upload(ctx, task)
	info, statErr := os.Stat(task.localPath)
	s.mu.Lock()
	task.syncing = false
	task.stableSince = time.Time{}
	if err == nil && statErr == nil {
		task.lastSize = info.Size()
		task.lastModTime = info.ModTime()
	} else if isConflict(err) {
		task.conflicted = true
	}
	s.mu.Unlock()
	if err != nil {
		s.emitEvent(Event{Type: "error", Path: task.input.Path, Message: err.Error()})
		return
	}
	s.emitEvent(Event{Type: "synced", Path: task.input.Path})
}

func (s *Service) upload(ctx context.Context, task *watchTask) error {
	file, err := os.Open(task.localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > MaxOpenFileSize {
		return fmt.Errorf("文件超过自动回传限制（%d MiB）", MaxOpenFileSize/1024/1024)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest == task.baseVersion {
		return nil
	}
	grant, err := s.authorizeUpload(ctx, task, info.Size(), digest)
	if err != nil {
		return err
	}
	endpoint, err := resolveGrantEndpoint(task.runtime.APIBaseURL, grant.Endpoint)
	if err != nil {
		return err
	}
	chunkSize := grant.ChunkSize
	if chunkSize <= 0 || chunkSize > 8*1024*1024 {
		chunkSize = 2 * 1024 * 1024
	}
	buffer := make([]byte, int(chunkSize))
	var offset int64
	for {
		read, readErr := io.ReadFull(file, buffer)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return fmt.Errorf("读取本地回传分块: %w", readErr)
		}
		final := offset+int64(read) == info.Size()
		if read == 0 && info.Size() > 0 {
			return errors.New("本地文件在回传期间发生截断")
		}
		if err := s.sendUploadChunk(ctx, task, grant, endpoint, buffer[:read], offset, final); err != nil {
			return err
		}
		offset += int64(read)
		if final {
			break
		}
	}
	task.baseVersion = digest
	return nil
}

func resolveGrantEndpoint(apiBaseURL, endpoint string) (string, error) {
	target, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if target.IsAbs() {
		return target.String(), nil
	}
	base, err := url.Parse(strings.TrimRight(apiBaseURL, "/") + "/")
	if err != nil {
		return "", err
	}
	return base.ResolveReference(target).String(), nil
}

func (s *Service) sendUploadChunk(ctx context.Context, task *watchTask, uploadGrant grant, endpoint string, chunk []byte, offset int64, final bool) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		// 路径经 URL query 传递（百分号编码，支持中文等非 Latin-1 字符）：
		// X-Prism-Path header 会被 Go 客户端在写请求时拒绝非 ASCII 值。
		targetURL, err := url.Parse(endpoint)
		if err != nil {
			return err
		}
		query := targetURL.Query()
		query.Set("path", uploadGrant.Path)
		targetURL.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL.String(), bytes.NewReader(chunk))
		if err != nil {
			return err
		}
		request.ContentLength = int64(len(chunk))
		request.Header.Set("Authorization", "Bearer "+uploadGrant.Ticket)
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set("X-Prism-Resource-Type", uploadGrant.ResourceType)
		request.Header.Set("X-Prism-Resource-ID", uploadGrant.ResourceID)
		request.Header.Set("X-Prism-Overwrite", "true")
		request.Header.Set("X-Prism-Expected-Version", task.baseVersion)
		request.Header.Set("X-Prism-Upload-Offset", fmt.Sprintf("%d", offset))
		request.Header.Set("X-Prism-Upload-Final", fmt.Sprintf("%t", final))
		if uploadGrant.Mode == "proxy" {
			request.Header.Set("X-Prism-Client-Session", task.runtime.ProxySession)
		}
		response, requestErr := s.client.Do(request)
		if requestErr != nil {
			lastErr = fmt.Errorf("回传本地文件分块: %w", requestErr)
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			response.Body.Close()
			return nil
		}
		lastErr = decodeResponseError(response)
		status := response.StatusCode
		response.Body.Close()
		if isConflict(lastErr) || status >= 400 && status < 500 && status != http.StatusTooManyRequests {
			return lastErr
		}
	}
	return lastErr
}

func (s *Service) authorizeUpload(ctx context.Context, task *watchTask, size int64, digest string) (grant, error) {
	body, err := json.Marshal(map[string]any{
		"node_id": task.input.NodeID, "scope": "file.upload",
		"resource_type": task.input.ResourceType, "resource_id": task.input.ResourceID,
		"path": task.input.Path, "paths": []string{task.input.Path},
		"size": size, "sha256": digest, "overwrite": true,
		"expected_version": task.baseVersion,
		"chunked":          true,
	})
	if err != nil {
		return grant{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(task.runtime.APIBaseURL, "/")+"/api/v1/files/authorize", bytes.NewReader(body))
	if err != nil {
		return grant{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Prism-Client-Session", task.runtime.ProxySession)
	response, err := s.client.Do(request)
	if err != nil {
		return grant{}, err
	}
	defer response.Body.Close()
	var payload struct {
		Success bool   `json:"success"`
		Data    grant  `json:"data"`
		Error   apiErr `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return grant{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !payload.Success {
		return grant{}, payload.Error.asError("文件回传授权失败")
	}
	return payload.Data, nil
}

type apiErr struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e apiErr) Error() string { return e.Message }
func (e apiErr) asError(fallback string) error {
	if e.Message == "" {
		return errors.New(fallback)
	}
	return e
}

func decodeResponseError(response *http.Response) error {
	var payload struct {
		Error apiErr `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&payload); err == nil && payload.Error.Message != "" {
		return payload.Error
	}
	return fmt.Errorf("远程文件请求失败（HTTP %d）", response.StatusCode)
}

func isConflict(err error) bool {
	var apiError apiErr
	return errors.As(err, &apiError) && apiError.Code == "FILE_CHANGED"
}

func (s *Service) emitEvent(event Event) {
	if s.emit != nil {
		s.emit(event)
	}
}
