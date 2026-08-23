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
}

type pendingStore struct {
	root string
	mu   sync.Mutex
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

func (s *pendingStore) apply(instanceID string, apply func(pendingOperation, string) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	directory, err := s.instanceDirectory(instanceID)
	if err != nil {
		return err
	}
	items, err := s.loadLocked(directory)
	if err != nil {
		return err
	}
	for index, operation := range items {
		bundlePath := ""
		if operation.BundleFile != "" {
			bundlePath = filepath.Join(directory, operation.BundleFile)
		}
		if err := apply(operation, bundlePath); err != nil {
			return err
		}
		if bundlePath != "" {
			_ = os.Remove(bundlePath)
		}
		if err := s.saveLocked(directory, items[index+1:]); err != nil {
			return err
		}
	}
	return nil
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
