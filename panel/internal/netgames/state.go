package netgames

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	settingsFile = "settings.yaml"
)

// Settings 是国际版（Minecraft Java 版服务器监控）相关的持久化设置，
// 存放于 settings.yaml。
type Settings struct {
	// HistoryRetentionDays 控制国际服历史观察点的保留天数。
	HistoryRetentionDays int `yaml:"history_retention_days" json:"history_retention_days"`
	// MinecraftCollectionIntervalMinutes 控制国际服定时采集间隔（分钟）。
	MinecraftCollectionIntervalMinutes int `yaml:"mc_collection_interval_minutes" json:"mc_collection_interval_minutes"`
}

type StateStore struct {
	dir      string
	mu       sync.RWMutex
	settings Settings
}

func DefaultSettings() Settings {
	return Settings{
		HistoryRetentionDays:               30,
		MinecraftCollectionIntervalMinutes: 1,
	}
}

func NormalizeSettings(settings Settings) Settings {
	defaults := DefaultSettings()
	if settings.HistoryRetentionDays < 1 || settings.HistoryRetentionDays > 3650 {
		settings.HistoryRetentionDays = defaults.HistoryRetentionDays
	}
	if settings.MinecraftCollectionIntervalMinutes < 1 || settings.MinecraftCollectionIntervalMinutes > 60 {
		settings.MinecraftCollectionIntervalMinutes = defaults.MinecraftCollectionIntervalMinutes
	}
	return settings
}

func (s Settings) Validate() error {
	if s.HistoryRetentionDays < 1 || s.HistoryRetentionDays > 3650 {
		return fmt.Errorf("history_retention_days must be between 1 and 3650")
	}
	if s.MinecraftCollectionIntervalMinutes < 1 || s.MinecraftCollectionIntervalMinutes > 60 {
		return fmt.Errorf("mc_collection_interval_minutes must be between 1 and 60")
	}
	return nil
}

func NewStateStore(baseDir string) (*StateStore, error) {
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return nil, err
	}
	state := &StateStore{dir: baseDir, settings: DefaultSettings()}
	if loaded, err := state.loadSettings(); err == nil {
		state.settings = NormalizeSettings(loaded)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return state, nil
}

func (s *StateStore) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *StateStore) UpdateSettings(settings Settings) error {
	settings = NormalizeSettings(settings)
	if err := settings.Validate(); err != nil {
		return err
	}
	encoded, err := yaml.Marshal(settings)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(filepath.Join(s.dir, settingsFile), encoded, 0o640); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = settings
	s.mu.Unlock()
	return nil
}

func (s *StateStore) loadSettings() (Settings, error) {
	contents, err := os.ReadFile(filepath.Join(s.dir, settingsFile))
	if err != nil {
		return Settings{}, err
	}
	settings := DefaultSettings()
	if err := yaml.Unmarshal(contents, &settings); err != nil {
		return Settings{}, err
	}
	return NormalizeSettings(settings), nil
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
