package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"PrismPanel-daemon/internal/atomicfile"
	"PrismPanel-daemon/internal/model"
)

type LoadError struct {
	Path string `json:"path"`
	Err  error  `json:"-"`
}

func (e LoadError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }

type ServerStore struct {
	dir string
	mu  sync.RWMutex
}

func NewServerStore(dataDir string) *ServerStore {
	return &ServerStore{dir: filepath.Join(dataDir, "servers")}
}

func (s *ServerStore) LoadAll() ([]model.ServerConfig, []LoadError, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return nil, nil, fmt.Errorf("create servers directory: %w", err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read servers directory: %w", err)
	}
	configs := make([]model.ServerConfig, 0, len(entries))
	loadErrors := make([]LoadError, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		contents, err := os.ReadFile(path)
		if err != nil {
			loadErrors = append(loadErrors, LoadError{Path: path, Err: err})
			continue
		}
		var cfg model.ServerConfig
		if err := json.Unmarshal(contents, &cfg); err != nil {
			loadErrors = append(loadErrors, LoadError{Path: path, Err: err})
			continue
		}
		cfg.Normalize()
		if err := cfg.Validate(); err != nil {
			loadErrors = append(loadErrors, LoadError{Path: path, Err: err})
			continue
		}
		if entry.Name() != cfg.ServerID+".json" {
			loadErrors = append(loadErrors, LoadError{Path: path, Err: errors.New("filename does not match server_id")})
			continue
		}
		configs = append(configs, cfg)
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].ServerID < configs[j].ServerID })
	return configs, loadErrors, nil
}

func (s *ServerStore) Save(cfg model.ServerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode server config: %w", err)
	}
	contents = append(contents, '\n')
	path := filepath.Join(s.dir, cfg.ServerID+".json")
	if err := atomicfile.WriteFile(path, contents, 0o640); err != nil {
		return fmt.Errorf("write server config: %w", err)
	}
	return nil
}

func (s *ServerStore) Delete(serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(filepath.Join(s.dir, serverID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
