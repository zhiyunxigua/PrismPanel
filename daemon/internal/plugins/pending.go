package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"PrismPanel-daemon/internal/atomicfile"
)

// pendingRetryThreshold 是 pending 队列项的连续重试上限：
// transient（文件占用/权限等可重试）失败在阈值内保留在队列等待下次启动重试，
// 达到阈值后视为永久失败移入 failed 侧写并跳过，避免队列无限毒化阻塞实例启动。
const pendingRetryThreshold = 3

type pendingOperation struct {
	Type             string    `json:"type"`
	PluginType       string    `json:"plugin_type,omitempty"`
	PluginName       string    `json:"plugin_name,omitempty"`
	OriginalFilename string    `json:"original_filename,omitempty"`
	ConfigDirectory  string    `json:"config_directory,omitempty"`
	DeleteConfig     bool      `json:"delete_config,omitempty"`
	Directory        string    `json:"directory,omitempty"`
	BundleFile       string    `json:"bundle_file,omitempty"`
	BackupSnapshot   bool      `json:"backup_snapshot,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	// Attempts 记录该条在 apply 阶段连续失败的次数（持久化，跨守护进程重启保留）。
	Attempts int `json:"attempts,omitempty"`
	// LastError 是最近一次 apply 失败的原始错误信息。
	LastError string `json:"last_error,omitempty"`
	// FailedAt 非零表示该条已移入 failed 侧写（永久失败或重试达阈值）。
	FailedAt time.Time `json:"failed_at,omitempty"`
}

type pendingStore struct {
	root string
	mu   sync.Mutex
}

// pendingView 是单个实例的队列汇总（listAll 内部使用）。
type pendingView struct {
	InstanceID string             `json:"instance_id"`
	Pending    []pendingOperation `json:"pending"`
	Failed     []pendingOperation `json:"failed"`
}

func newPendingStore(root string) (*pendingStore, error) {
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create plugin pending directory: %w", err)
	}
	return &pendingStore{root: root}, nil
}

func (s *pendingStore) enqueue(instanceID string, operation pendingOperation, bundlePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := s.instanceDirectory(instanceID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	if bundlePath != "" {
		extension := ".zip"
		if operation.Type == "upload" {
			extension = ".jar"
		}
		name := fmt.Sprintf("bundle-%d%s", time.Now().UnixNano(), extension)
		if err := copyFile(bundlePath, filepath.Join(directory, name)); err != nil {
			return err
		}
		operation.BundleFile = name
	}
	operation.CreatedAt = time.Now().UTC()
	items, err := s.loadLocked(directory)
	if err != nil {
		return err
	}
	items = append(items, operation)
	return s.saveLocked(directory, items)
}

// apply 逐条执行实例的 pending 队列。单条失败不再整体中止：
//   - 成功项直接出队（并清理 bundle 文件）；
//   - 可重试失败（retryableFileError）且连续失败次数未达阈值时保留在队列，
//     记录 Attempts/LastError，供下次启动重试；
//   - 永久失败或重试达阈值时移入 failed 侧写（failed.json）并跳过，继续执行后续项。
// 返回 drained：true 表示队列已清空（全部成功或全部跳过），
// 调用方仅在 drained 时允许实例正常启动，否则应阻止启动并在下次启动重试。
func (s *pendingStore) apply(instanceID string, apply func(pendingOperation, string) error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := s.instanceDirectory(instanceID)
	if err != nil {
		return false, err
	}
	items, err := s.loadLocked(directory)
	if err != nil {
		return false, err
	}
	remaining := make([]pendingOperation, 0, len(items))
	var failed []pendingOperation
	for _, operation := range items {
		bundlePath := ""
		if operation.BundleFile != "" {
			bundlePath = filepath.Join(directory, operation.BundleFile)
		}
		applyErr := apply(operation, bundlePath)
		if applyErr == nil {
			if bundlePath != "" {
				_ = os.Remove(bundlePath)
			}
			continue
		}
		operation.Attempts++
		operation.LastError = applyErr.Error()
		if retryableFileError(applyErr) && operation.Attempts < pendingRetryThreshold {
			remaining = append(remaining, operation)
			continue
		}
		operation.FailedAt = time.Now().UTC()
		failed = append(failed, operation)
		if bundlePath != "" {
			_ = os.Remove(bundlePath)
		}
	}
	if err := s.saveLocked(directory, remaining); err != nil {
		return false, err
	}
	if len(failed) > 0 {
		if err := s.appendFailedLocked(directory, failed); err != nil {
			return false, err
		}
	}
	return len(remaining) == 0, nil
}

func (s *pendingStore) instanceDirectory(instanceID string) (string, error) {
	if instanceID == "" || instanceID == "." || instanceID == ".." ||
		filepath.Base(instanceID) != instanceID || strings.ContainsAny(instanceID, "\\/") {
		return "", errors.New("invalid instance id")
	}
	return filepath.Join(s.root, instanceID), nil
}

func (s *pendingStore) loadLocked(directory string) ([]pendingOperation, error) {
	data, err := os.ReadFile(filepath.Join(directory, "pending.json"))
	if errors.Is(err, os.ErrNotExist) {
		return []pendingOperation{}, nil
	}
	if err != nil {
		return nil, err
	}
	var items []pendingOperation
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("decode pending plugin operations: %w", err)
	}
	return items, nil
}

func (s *pendingStore) saveLocked(directory string, items []pendingOperation) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, byte(10))
	return atomicfile.WriteFile(filepath.Join(directory, "pending.json"), data, 0o640)
}

// loadFailedLocked 读取实例的 failed 侧写（永久失败/重试达阈值的操作）。
func (s *pendingStore) loadFailedLocked(directory string) ([]pendingOperation, error) {
	data, err := os.ReadFile(filepath.Join(directory, "failed.json"))
	if errors.Is(err, os.ErrNotExist) {
		return []pendingOperation{}, nil
	}
	if err != nil {
		return nil, err
	}
	var items []pendingOperation
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("decode failed plugin operations: %w", err)
	}
	return items, nil
}

func (s *pendingStore) saveFailedLocked(directory string, items []pendingOperation) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, byte(10))
	return atomicfile.WriteFile(filepath.Join(directory, "failed.json"), data, 0o640)
}

func (s *pendingStore) appendFailedLocked(directory string, items []pendingOperation) error {
	existing, err := s.loadFailedLocked(directory)
	if err != nil {
		return err
	}
	existing = append(existing, items...)
	return s.saveFailedLocked(directory, existing)
}

// list 返回实例的 pending 队列与 failed 侧写。
func (s *pendingStore) list(instanceID string) ([]pendingOperation, []pendingOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := s.instanceDirectory(instanceID)
	if err != nil {
		return nil, nil, err
	}
	pending, err := s.loadLocked(directory)
	if err != nil {
		return nil, nil, err
	}
	failed, err := s.loadFailedLocked(directory)
	if err != nil {
		return nil, nil, err
	}
	return pending, failed, nil
}

// listAll 返回全部存在 pending/failed 记录的实例队列（诊断用）。
func (s *pendingStore) listAll() (map[string]pendingView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	views := make(map[string]pendingView)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instanceID := entry.Name()
		directory := filepath.Join(s.root, instanceID)
		pending, loadErr := s.loadLocked(directory)
		if loadErr != nil {
			return nil, loadErr
		}
		failed, loadErr := s.loadFailedLocked(directory)
		if loadErr != nil {
			return nil, loadErr
		}
		if len(pending) == 0 && len(failed) == 0 {
			continue
		}
		views[instanceID] = pendingView{InstanceID: instanceID, Pending: pending, Failed: failed}
	}
	return views, nil
}

// clear 清除实例的 pending 队列与 failed 侧写。
// index/failedIndex 均为 nil 时清空整个实例队列目录（含 bundle 文件）；
// 否则按 0 起始下标删除单条（index 针对 pending 队列，failedIndex 针对 failed 侧写）。
func (s *pendingStore) clear(instanceID string, index, failedIndex *int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := s.instanceDirectory(instanceID)
	if err != nil {
		return err
	}
	if index == nil && failedIndex == nil {
		return os.RemoveAll(directory)
	}
	items, err := s.loadLocked(directory)
	if err != nil {
		return err
	}
	if index != nil {
		if *index < 0 || *index >= len(items) {
			return errors.New("pending queue index out of range")
		}
		if items[*index].BundleFile != "" {
			_ = os.Remove(filepath.Join(directory, items[*index].BundleFile))
		}
		items = append(items[:*index], items[*index+1:]...)
		if err := s.saveLocked(directory, items); err != nil {
			return err
		}
	}
	if failedIndex != nil {
		failed, err := s.loadFailedLocked(directory)
		if err != nil {
			return err
		}
		if *failedIndex < 0 || *failedIndex >= len(failed) {
			return errors.New("failed queue index out of range")
		}
		failed = append(failed[:*failedIndex], failed[*failedIndex+1:]...)
		if err := s.saveFailedLocked(directory, failed); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destination)
	}
	return closeErr
}
