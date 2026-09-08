package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"PrismPanel/internal/daemon"
)

type fileSyncResource struct {
	NodeID       string `json:"node_id"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
}

type fileSyncPath struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type fileSyncTarget struct {
	NodeID     string `json:"node_id"`
	InstanceID string `json:"instance_id"`
}

type fileSyncRequest struct {
	Source  fileSyncResource `json:"source"`
	Paths   []fileSyncPath   `json:"paths"`
	Targets []fileSyncTarget `json:"targets"`
}

type fileSyncItem struct {
	ID             string     `json:"id"`
	NodeID         string     `json:"node_id"`
	InstanceID     string     `json:"instance_id"`
	Path           string     `json:"path"`
	Type           string     `json:"type"`
	Status         string     `json:"status"`
	Stage          string     `json:"stage,omitempty"`
	ErrorCode      string     `json:"error_code,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	ErrorDetails   []string   `json:"error_details,omitempty"`
	Retryable      bool       `json:"retryable,omitempty"`
	RetryAfterStop bool       `json:"retry_after_stop,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type fileSyncSnapshot struct {
	TaskID     string           `json:"task_id"`
	Source     fileSyncResource `json:"source"`
	Paths      []fileSyncPath   `json:"paths"`
	Targets    []fileSyncTarget `json:"targets"`
	Status     string           `json:"status"`
	Total      int              `json:"total"`
	Completed  int              `json:"completed"`
	Failed     int              `json:"failed"`
	CreatedAt  time.Time        `json:"created_at"`
	StartedAt  *time.Time       `json:"started_at,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Error      string           `json:"error,omitempty"`
	Items      []fileSyncItem   `json:"items"`
}

type fileSyncTask struct {
	mu      sync.RWMutex
	snap    fileSyncSnapshot
	request fileSyncRequest
	cancel  context.CancelFunc
}

type syncSourceFile struct {
	Path string
	Type string
}

type syncListEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

func (s *Server) handleFileSyncs(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	var input fileSyncRequest
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	if err == nil {
		err = validateFileSyncRequest(&input)
	}
	if err == nil {
		err = s.authorizeFileScope(request, "file.read", input.Source.NodeID, input.Source.ResourceType, input.Source.ResourceID)
	}
	if err == nil {
		for _, target := range input.Targets {
			if target.NodeID == input.Source.NodeID && target.InstanceID == input.Source.ResourceID && input.Source.ResourceType == "instance" {
				err = apiError("INVALID_REQUEST", "不能同步到源实例")
				break
			}
			if targetErr := s.authorizeFileScope(request, "file.write", target.NodeID, "instance", target.InstanceID); targetErr != nil {
				err = targetErr
				break
			}
		}
	}
	if err != nil {
		writeError(writer, err)
		return
	}

	id := "file-sync-" + randomFileSyncID()
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	task := &fileSyncTask{
		snap: fileSyncSnapshot{
			TaskID: id, Source: input.Source, Paths: append([]fileSyncPath(nil), input.Paths...),
			Targets: append([]fileSyncTarget(nil), input.Targets...), Status: "queued", CreatedAt: now,
		},
		request: input, cancel: cancel,
	}
	s.fileSyncMu.Lock()
	s.fileSync[id] = task
	s.fileSyncMu.Unlock()
	go s.runFileSync(task, ctx, nil)
	writeSuccess(writer, task.snapshot())
}

func (s *Server) handleFileSync(writer http.ResponseWriter, request *http.Request) {
	pathValue := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/file-syncs/"), "/")
	parts := strings.Split(pathValue, "/")
	if len(parts) < 1 || parts[0] == "" || len(parts) > 2 {
		http.NotFound(writer, request)
		return
	}
	s.fileSyncMu.RLock()
	task := s.fileSync[parts[0]]
	s.fileSyncMu.RUnlock()
	if task == nil {
		writeError(writer, apiError("NOT_FOUND", "同步任务不存在"))
		return
	}
	if len(parts) == 1 && request.Method == http.MethodGet {
		writeSuccess(writer, task.snapshot())
		return
	}
	if len(parts) == 2 && parts[1] == "retry" && request.Method == http.MethodPost {
		if err := s.authorizeFileScope(request, "file.read", task.request.Source.NodeID, task.request.Source.ResourceType, task.request.Source.ResourceID); err != nil {
			writeError(writer, err)
			return
		}
		for _, target := range task.request.Targets {
			if err := s.authorizeFileScope(request, "file.write", target.NodeID, "instance", target.InstanceID); err != nil {
				writeError(writer, err)
				return
			}
		}
		task.retry(s)
		writeSuccess(writer, task.snapshot())
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && request.Method == http.MethodPost {
		task.cancelTask()
		writeSuccess(writer, task.snapshot())
		return
	}
	methodNotAllowed(writer, "GET, POST")
}

func validateFileSyncRequest(input *fileSyncRequest) error {
	if input == nil {
		return apiError("INVALID_REQUEST", "同步请求不能为空")
	}
	input.Source.NodeID = strings.TrimSpace(input.Source.NodeID)
	input.Source.ResourceType = strings.TrimSpace(input.Source.ResourceType)
	input.Source.ResourceID = strings.TrimSpace(input.Source.ResourceID)
	if input.Source.NodeID == "" || input.Source.ResourceID == "" || (input.Source.ResourceType != "instance" && input.Source.ResourceType != "image") {
		return apiError("INVALID_REQUEST", "同步源资源无效")
	}
	if len(input.Paths) == 0 || len(input.Paths) > 100 {
		return apiError("INVALID_REQUEST", "同步路径数量必须在 1 到 100 之间")
	}
	seen := make(map[string]struct{}, len(input.Paths))
	for index := range input.Paths {
		clean, err := normalizeSyncPath(input.Paths[index].Path)
		if err != nil {
			return err
		}
		kind := strings.ToLower(strings.TrimSpace(input.Paths[index].Type))
		if kind != "file" && kind != "directory" {
			return apiError("INVALID_REQUEST", "同步路径类型必须是 file 或 directory")
		}
		input.Paths[index].Path, input.Paths[index].Type = clean, kind
		if _, exists := seen[clean]; exists {
			return apiError("INVALID_REQUEST", "同步路径不能重复")
		}
		seen[clean] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for value := range seen {
		paths = append(paths, value)
	}
	sort.Strings(paths)
	for index := 1; index < len(paths); index++ {
		if strings.HasPrefix(paths[index], paths[index-1]+"/") {
			return apiError("INVALID_REQUEST", "不能同时选择父目录和其中的子路径")
		}
	}
	if len(input.Targets) == 0 || len(input.Targets) > 100 {
		return apiError("INVALID_REQUEST", "同步目标数量必须在 1 到 100 之间")
	}
	targets := make(map[string]struct{}, len(input.Targets))
	for index := range input.Targets {
		target := &input.Targets[index]
		target.NodeID = strings.TrimSpace(target.NodeID)
		target.InstanceID = strings.TrimSpace(target.InstanceID)
		if target.NodeID == "" || target.InstanceID == "" {
			return apiError("INVALID_REQUEST", "同步目标实例无效")
		}
		key := target.NodeID + "\x00" + target.InstanceID
		if _, exists := targets[key]; exists {
			return apiError("INVALID_REQUEST", "同步目标不能重复")
		}
		targets[key] = struct{}{}
	}
	return nil
}

func normalizeSyncPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "../") || value == ".." || path.IsAbs(value) || (len(value) >= 2 && value[1] == ':') {
		return "", apiError("INVALID_REQUEST", "同步路径必须是工作目录内的相对路径")
	}
	value = strings.Trim(value, "/")
	if value == "" {
		return ".", nil
	}
	clean := path.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, ".prism-recycle-bin") {
		return "", apiError("INVALID_REQUEST", "同步路径无效")
	}
	return clean, nil
}

func (task *fileSyncTask) snapshot() fileSyncSnapshot {
	task.mu.RLock()
	defer task.mu.RUnlock()
	value := task.snap
	value.Paths = append([]fileSyncPath(nil), value.Paths...)
	value.Targets = append([]fileSyncTarget(nil), value.Targets...)
	value.Items = append([]fileSyncItem(nil), value.Items...)
	return value
}

func (task *fileSyncTask) cancelTask() {
	task.mu.Lock()
	if task.cancel != nil && task.snap.Status != "completed" && task.snap.Status != "completed_with_errors" && task.snap.Status != "failed" && task.snap.Status != "cancelled" {
		task.snap.Status = "cancel_requested"
		task.cancel()
	}
	task.mu.Unlock()
}

func (task *fileSyncTask) retry(server *Server) {
	task.mu.Lock()
	if task.snap.Status == "running" || task.snap.Status == "queued" {
		task.mu.Unlock()
		return
	}
	failed := make(map[string]struct{})
	for _, item := range task.snap.Items {
		if item.Status == "failed" {
			failed[item.ID] = struct{}{}
		}
	}
	if len(failed) == 0 {
		task.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	task.cancel = cancel
	task.snap.Status = "queued"
	task.snap.Error = ""
	for index := range task.snap.Items {
		if _, exists := failed[task.snap.Items[index].ID]; exists {
			task.snap.Items[index].Status = "queued"
			task.snap.Items[index].ErrorCode = ""
			task.snap.Items[index].ErrorMessage = ""
			task.snap.Items[index].ErrorDetails = nil
			task.snap.Items[index].Stage = ""
		}
	}
	task.mu.Unlock()
	go server.runFileSync(task, ctx, failed)
}

func (s *Server) runFileSync(task *fileSyncTask, ctx context.Context, only map[string]struct{}) {
	now := time.Now().UTC()
	task.mu.Lock()
	task.snap.Status = "running"
	task.snap.StartedAt = &now
	task.mu.Unlock()
	entries, err := s.expandSyncPaths(ctx, task.request.Source, task.request.Paths)
	if err != nil {
		task.mu.Lock()
		task.snap.Status = "failed"
		task.snap.Error = err.Error()
		task.snap.FinishedAt = timePtr(time.Now().UTC())
		for index := range task.snap.Items {
			if task.snap.Items[index].Status == "queued" {
				task.applyErrorLocked(&task.snap.Items[index], "listing", err)
			}
		}
		task.mu.Unlock()
		return
	}

	task.mu.Lock()
	if only == nil {
		task.snap.Total = len(entries) * len(task.request.Targets)
		task.snap.Items = make([]fileSyncItem, 0, len(entries)*len(task.request.Targets))
		for _, target := range task.request.Targets {
			for _, entry := range entries {
				task.snap.Items = append(task.snap.Items, newFileSyncItem(target, entry))
			}
		}
	}
	task.mu.Unlock()

	for _, target := range task.request.Targets {
		for _, entry := range entries {
			id := fileSyncItemID(target, entry)
			if only != nil {
				if _, exists := only[id]; !exists {
					continue
				}
			}
			if ctx.Err() != nil {
				task.finishCancelled()
				return
			}
			task.markItem(id, "running", "preparing", nil)
			err := s.transferSyncEntry(ctx, task.request.Source, target, entry, func(stage string) {
				task.markItem(id, "running", stage, nil)
			})
			if err != nil {
				task.markItem(id, "failed", "failed", err)
			} else {
				task.markItem(id, "completed", "completed", nil)
			}
		}
	}
	task.mu.Lock()
	if task.snap.Status == "cancel_requested" || ctx.Err() != nil {
		task.snap.Status = "cancelled"
	} else {
		task.snap.Completed, task.snap.Failed = 0, 0
		for _, item := range task.snap.Items {
			if item.Status == "completed" {
				task.snap.Completed++
			}
			if item.Status == "failed" {
				task.snap.Failed++
			}
		}
		if task.snap.Failed > 0 {
			task.snap.Status = "completed_with_errors"
		} else {
			task.snap.Status = "completed"
		}
	}
	task.snap.FinishedAt = timePtr(time.Now().UTC())
	task.mu.Unlock()
}

func (s *Server) expandSyncPaths(ctx context.Context, source fileSyncResource, paths []fileSyncPath) ([]syncSourceFile, error) {
	result := make([]syncSourceFile, 0)
	for _, selected := range paths {
		if selected.Type == "file" {
			result = append(result, syncSourceFile{Path: selected.Path, Type: "file"})
			continue
		}
		result = append(result, syncSourceFile{Path: selected.Path, Type: "directory"})
		if err := s.walkSyncDirectory(ctx, source, selected.Path, &result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Server) walkSyncDirectory(ctx context.Context, source fileSyncResource, directory string, result *[]syncSourceFile) error {
	entries, err := s.listSyncDirectory(ctx, source, directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type == "directory" {
			*result = append(*result, syncSourceFile{Path: entry.Path, Type: "directory"})
			if err := s.walkSyncDirectory(ctx, source, entry.Path, result); err != nil {
				return err
			}
		} else {
			*result = append(*result, syncSourceFile{Path: entry.Path, Type: "file"})
		}
	}
	return nil
}

func (s *Server) listSyncDirectory(ctx context.Context, source fileSyncResource, directory string) ([]syncListEntry, error) {
	result := make([]syncListEntry, 0)
	cursor := ""
	for {
		ticket, err := s.createSyncTicket(ctx, source.NodeID, map[string]any{
			"scope": "file.list", "resource_type": source.ResourceType, "resource_id": source.ResourceID,
			"path": ".", "ttl_seconds": 300,
		})
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(map[string]any{"directories": []map[string]any{{"path": directory, "cursor": cursor, "limit": 500}}, "include_hidden": true})
		response, err := s.fileRequest(ctx, source.NodeID, "list", http.MethodPost, syncHeaders(ticket, source, "."), strings.NewReader(string(body)), int64(len(body)))
		if err != nil {
			return nil, err
		}
		var payload struct {
			Success bool `json:"success"`
			Data    struct {
				Results []struct {
					Entries    []syncListEntry `json:"entries"`
					NextCursor string          `json:"next_cursor"`
					Truncated  bool            `json:"truncated"`
				} `json:"results"`
			} `json:"data"`
			Error *daemon.APIError `json:"error"`
		}
		err = json.NewDecoder(response.Body).Decode(&payload)
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		if !payload.Success || payload.Error != nil {
			if payload.Error != nil {
				return nil, payload.Error
			}
			return nil, errors.New("源目录读取失败")
		}
		if len(payload.Data.Results) == 0 {
			return nil, errors.New("源目录读取结果为空")
		}
		page := payload.Data.Results[0]
		result = append(result, page.Entries...)
		if !page.Truncated || page.NextCursor == "" {
			return result, nil
		}
		cursor = page.NextCursor
	}
}

func (s *Server) transferSyncEntry(ctx context.Context, source fileSyncResource, target fileSyncTarget, entry syncSourceFile, progress func(string)) error {
	if entry.Type == "directory" {
		progress("creating_directory")
		ticket, err := s.createSyncTicket(ctx, target.NodeID, map[string]any{
			"scope": "file.create", "resource_type": "instance", "resource_id": target.InstanceID,
			"path": entry.Path, "ttl_seconds": 300,
		})
		if err != nil {
			return err
		}
		body := strings.NewReader(`{"type":"directory"}`)
		response, err := s.fileRequest(ctx, target.NodeID, "create", http.MethodPost, syncHeaders(ticket, fileSyncResource{ResourceType: "instance", ResourceID: target.InstanceID}, entry.Path), body, int64(len(`{"type":"directory"}`)))
		if err != nil {
			return err
		}
		return decodeSyncResponse(response)
	}
	progress("downloading")
	temp, err := os.CreateTemp("", ".prism-file-sync-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	var hash = sha256.New()
	ticket, err := s.createSyncTicket(ctx, source.NodeID, map[string]any{
		"scope": "file.download", "resource_type": source.ResourceType, "resource_id": source.ResourceID,
		"path": entry.Path, "ttl_seconds": 300,
	})
	if err == nil {
		response, requestErr := s.fileRequest(ctx, source.NodeID, "download", http.MethodGet, syncHeaders(ticket, source, entry.Path), nil, 0)
		if requestErr != nil {
			err = requestErr
		} else {
			_, copyErr := io.Copy(io.MultiWriter(temp, hash), response.Body)
			closeErr := response.Body.Close()
			err = errors.Join(copyErr, closeErr)
		}
	}
	if err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	info, err := os.Stat(tempPath)
	if err != nil {
		return err
	}
	progress("uploading")
	targetTicket, err := s.createSyncTicket(ctx, target.NodeID, map[string]any{
		"scope": "file.upload", "resource_type": "instance", "resource_id": target.InstanceID,
		"path": entry.Path, "size": info.Size(), "sha256": hex.EncodeToString(hash.Sum(nil)),
		"overwrite": true, "ttl_seconds": 900,
	})
	if err != nil {
		return err
	}
	file, err := os.Open(tempPath)
	if err != nil {
		return err
	}
	defer file.Close()
	response, err := s.fileRequest(ctx, target.NodeID, "upload", http.MethodPost, syncHeaders(targetTicket, fileSyncResource{ResourceType: "instance", ResourceID: target.InstanceID}, entry.Path, true), file, info.Size())
	if err != nil {
		return err
	}
	return decodeSyncResponse(response)
}

func (s *Server) createSyncTicket(ctx context.Context, nodeID string, input map[string]any) (string, error) {
	var result struct {
		Ticket string `json:"ticket"`
	}
	if err := s.connections.Call(ctx, nodeID, "ticket.create", input, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Ticket) == "" {
		return "", errors.New("daemon 未返回文件凭证")
	}
	return result.Ticket, nil
}

func (s *Server) fileRequest(ctx context.Context, nodeID, operation, method string, headers http.Header, body io.Reader, contentLength int64) (*http.Response, error) {
	response, err := s.connections.FileRequest(ctx, nodeID, operation, method, headers, body, contentLength)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		var payload struct {
			Error *daemon.APIError `json:"error"`
		}
		if decodeErr := json.NewDecoder(response.Body).Decode(&payload); decodeErr == nil && payload.Error != nil {
			return nil, payload.Error
		}
		return nil, fmt.Errorf("文件操作失败（HTTP %d）", response.StatusCode)
	}
	return response, nil
}

func decodeSyncResponse(response *http.Response) error {
	defer response.Body.Close()
	var payload struct {
		Success bool             `json:"success"`
		Error   *daemon.APIError `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return err
	}
	if payload.Error != nil {
		return payload.Error
	}
	if !payload.Success {
		return errors.New("文件操作失败")
	}
	return nil
}

func syncHeaders(ticket string, resource fileSyncResource, pathValue string, overwrite ...bool) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+ticket)
	headers.Set("X-Prism-Resource-Type", resource.ResourceType)
	headers.Set("X-Prism-Resource-ID", resource.ResourceID)
	headers.Set("X-Prism-Path", pathValue)
	if len(overwrite) > 0 && overwrite[0] {
		headers.Set("X-Prism-Overwrite", "true")
	}
	return headers
}

func newFileSyncItem(target fileSyncTarget, entry syncSourceFile) fileSyncItem {
	return fileSyncItem{ID: fileSyncItemID(target, entry), NodeID: target.NodeID, InstanceID: target.InstanceID, Path: entry.Path, Type: entry.Type, Status: "queued"}
}

func fileSyncItemID(target fileSyncTarget, entry syncSourceFile) string {
	return target.NodeID + "\x00" + target.InstanceID + "\x00" + entry.Type + "\x00" + entry.Path
}

func (task *fileSyncTask) markItem(id, status, stage string, err error) {
	task.mu.Lock()
	defer task.mu.Unlock()
	for index := range task.snap.Items {
		if task.snap.Items[index].ID != id {
			continue
		}
		item := &task.snap.Items[index]
		item.Status, item.Stage = status, stage
		if status == "running" {
			item.StartedAt = timePtr(time.Now().UTC())
		}
		if status == "completed" || status == "failed" {
			item.FinishedAt = timePtr(time.Now().UTC())
		}
		if err != nil {
			task.applyErrorLocked(item, stage, err)
		}
		break
	}
}

func (task *fileSyncTask) applyErrorLocked(item *fileSyncItem, stage string, err error) {
	item.Stage = stage
	var daemonErr *daemon.APIError
	if errors.As(err, &daemonErr) {
		item.ErrorCode, item.ErrorMessage = daemonErr.Code, daemonErr.Message
		item.ErrorDetails, item.Retryable = append([]string(nil), daemonErr.Details...), daemonErr.Retryable
	} else {
		item.ErrorCode, item.ErrorMessage = "FILE_SYNC_FAILED", err.Error()
	}
	item.RetryAfterStop = item.ErrorCode == "FILE_WRITE_FAILED" || item.ErrorCode == "DISK_FULL" || item.ErrorCode == "READ_ONLY_FILESYSTEM"
}

func (task *fileSyncTask) finishCancelled() {
	task.mu.Lock()
	if task.snap.Status == "cancel_requested" {
		task.snap.Status = "cancelled"
	}
	task.snap.FinishedAt = timePtr(time.Now().UTC())
	task.mu.Unlock()
}

func randomFileSyncID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func timePtr(value time.Time) *time.Time { return &value }
