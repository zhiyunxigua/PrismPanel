package server

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
)

type Service struct {
	store      *store.ServerStore
	supervisor *supervisor.Manager
	mu         sync.RWMutex
	servers    map[string]model.ServerConfig
}

type ListResult struct {
	Servers   []model.ServerConfig  `json:"servers"`
	Instances []supervisor.Snapshot `json:"instances"`
}

func NewService(storage *store.ServerStore, manager *supervisor.Manager, configs []model.ServerConfig) *Service {
	servers := make(map[string]model.ServerConfig, len(configs))
	for _, cfg := range configs {
		servers[cfg.ServerID] = cfg
	}
	return &Service{store: storage, supervisor: manager, servers: servers}
}

func (s *Service) List() ListResult {
	s.mu.RLock()
	servers := make([]model.ServerConfig, 0, len(s.servers))
	for _, cfg := range s.servers {
		servers = append(servers, cfg)
	}
	s.mu.RUnlock()
	sort.Slice(servers, func(i, j int) bool { return servers[i].ServerID < servers[j].ServerID })
	return ListResult{Servers: servers, Instances: s.supervisor.List()}
}

func (s *Service) Get(serverID string) (model.ServerConfig, error) {
	s.mu.RLock()
	cfg, exists := s.servers[serverID]
	s.mu.RUnlock()
	if !exists {
		return model.ServerConfig{}, apperr.New("SERVER_NOT_FOUND", "服务器不存在")
	}
	return cfg, nil
}

func (s *Service) Create(cfg model.ServerConfig) ([]string, error) {
	cfg.Normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.servers[cfg.ServerID]; exists {
		return nil, apperr.New("SERVER_ID_CONFLICT", "服务器 ID 已存在")
	}
	if err := s.supervisor.ValidateServer(cfg, ""); err != nil {
		return nil, err
	}
	warnings, err := s.supervisor.ApplyServer(cfg)
	if err != nil {
		return nil, err
	}
	if err := ensureServerWorkspace(cfg); err != nil {
		_ = s.supervisor.RemoveServer(cfg.ServerID)
		return nil, err
	}
	if err := s.store.Save(cfg); err != nil {
		_ = s.supervisor.RemoveServer(cfg.ServerID)
		return nil, apperr.Wrap("CONFIG_WRITE_FAILED", "服务器配置保存失败", err)
	}
	s.servers[cfg.ServerID] = cfg
	return warnings, nil
}

func ensureServerWorkspace(cfg model.ServerConfig) error {
	target := cfg.Workspace
	if cfg.Type == "mirror" {
		target = filepath.Join(cfg.RootPath, cfg.ImageDirectory)
	}
	if !filepath.IsAbs(target) {
		return apperr.New("INVALID_CONFIG", "server workspace must be an absolute path")
	}
	absolute := filepath.Clean(target)
	missing := make([]string, 0)
	current := absolute
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return apperr.New("PATH_ESCAPE", "server workspace cannot contain symbolic links")
			}
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil || !samePath(current, resolved) {
				return apperr.New("PATH_ESCAPE", "server workspace cannot contain symbolic links")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return apperr.Wrap("FILE_WRITE_FAILED", "server workspace is unavailable", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return apperr.New("INVALID_CONFIG", "server workspace has no available parent directory")
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	for index := len(missing) - 1; index >= 0; index-- {
		current = filepath.Join(current, missing[index])
		if err := os.Mkdir(current, 0o750); err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "server workspace creation failed", err)
		}
	}
	return nil
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if filepath.Separator == 92 {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (s *Service) Update(serverID string, cfg model.ServerConfig) ([]string, error) {
	cfg.Normalize()
	if cfg.ServerID != serverID {
		return nil, apperr.New("SERVER_ID_IMMUTABLE", "服务器 ID 创建后不可修改")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.servers[serverID]
	if !exists {
		return nil, apperr.New("SERVER_NOT_FOUND", "服务器不存在")
	}
	if err := s.supervisor.ValidateServer(cfg, serverID); err != nil {
		return nil, err
	}
	warnings, err := s.supervisor.ApplyServer(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.store.Save(cfg); err != nil {
		_, _ = s.supervisor.ApplyServer(old)
		return nil, apperr.Wrap("CONFIG_WRITE_FAILED", "服务器配置保存失败", err)
	}
	s.servers[serverID] = cfg
	return warnings, nil
}

func (s *Service) Delete(serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.servers[serverID]
	if !exists {
		return apperr.New("SERVER_NOT_FOUND", "服务器不存在")
	}
	if err := s.supervisor.RemoveServer(serverID); err != nil {
		return err
	}
	if err := s.store.Delete(serverID); err != nil {
		_, _ = s.supervisor.ApplyServer(old)
		return apperr.Wrap("CONFIG_WRITE_FAILED", "服务器配置删除失败", err)
	}
	delete(s.servers, serverID)
	return nil
}
