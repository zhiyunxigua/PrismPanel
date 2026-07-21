package netgames

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"PrismPanel/internal/store"
	"gopkg.in/yaml.v3"
)

const (
	stateVersion = 1
	accountFile  = "account.enc"
	settingsFile = "settings.yaml"
)

type Settings struct {
	CollectionIntervalMinutes int `yaml:"collection_interval_minutes" json:"collection_interval_minutes"`
	HistoryRetentionDays      int `yaml:"history_retention_days" json:"history_retention_days"`
	DetailRefreshHours        int `yaml:"detail_refresh_hours" json:"detail_refresh_hours"`
	MaxDetailBatchSize        int `yaml:"max_detail_batch_size" json:"max_detail_batch_size"`
}

type DeviceState struct {
	UniqueID string `json:"unique_id"`
	ID       string `json:"id"`
	Key      string `json:"key"`
}

type AccountState struct {
	Email       string      `json:"email"`
	Password    string      `json:"password"`
	Device      DeviceState `json:"device"`
	VerifiedAt  *time.Time  `json:"verified_at,omitempty"`
	UpdatedAt   time.Time   `json:"updated_at"`
	CreatedAt   time.Time   `json:"created_at"`
	LastFailure string      `json:"last_failure,omitempty"`
}

type AccountView struct {
	Email      string     `json:"email"`
	HasAccount bool       `json:"has_account"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

type CollectorStatus struct {
	Running   bool                        `json:"running"`
	LastRun   *store.NetGameCollectionRun `json:"last_run,omitempty"`
	NextRunAt *time.Time                  `json:"next_run_at,omitempty"`
	Account   AccountView                 `json:"account"`
	Error     string                      `json:"error,omitempty"`
	Settings  Settings                    `json:"settings"`
}
type StateStore struct {
	dir      string
	aead     cipher.AEAD
	mu       sync.RWMutex
	settings Settings
	account  *AccountState
	loadErr  error
}

func DefaultSettings() Settings {
	return Settings{
		CollectionIntervalMinutes: 15,
		HistoryRetentionDays:      30,
		DetailRefreshHours:        24,
		MaxDetailBatchSize:        24,
	}
}

func NormalizeSettings(settings Settings) Settings {
	defaults := DefaultSettings()
	if settings.CollectionIntervalMinutes < 15 || settings.CollectionIntervalMinutes > 60 {
		settings.CollectionIntervalMinutes = defaults.CollectionIntervalMinutes
	}
	if settings.HistoryRetentionDays < 1 || settings.HistoryRetentionDays > 3650 {
		settings.HistoryRetentionDays = defaults.HistoryRetentionDays
	}
	if settings.DetailRefreshHours < 1 || settings.DetailRefreshHours > 168 {
		settings.DetailRefreshHours = defaults.DetailRefreshHours
	}
	if settings.MaxDetailBatchSize < 1 || settings.MaxDetailBatchSize > 100 {
		settings.MaxDetailBatchSize = defaults.MaxDetailBatchSize
	}
	return settings
}

func (s Settings) Validate() error {
	if s.CollectionIntervalMinutes < 15 || s.CollectionIntervalMinutes > 60 {
		return fmt.Errorf("collection_interval_minutes must be between 15 and 60")
	}
	if s.HistoryRetentionDays < 1 || s.HistoryRetentionDays > 3650 {
		return fmt.Errorf("history_retention_days must be between 1 and 3650")
	}
	if s.DetailRefreshHours < 1 || s.DetailRefreshHours > 168 {
		return fmt.Errorf("detail_refresh_hours must be between 1 and 168")
	}
	if s.MaxDetailBatchSize < 1 || s.MaxDetailBatchSize > 100 {
		return fmt.Errorf("max_detail_batch_size must be between 1 and 100")
	}
	return nil
}

func NewStateStore(baseDir string, masterKey []byte) (*StateStore, error) {
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("create net games state cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return nil, err
	}
	store := &StateStore{dir: baseDir, aead: aead, settings: DefaultSettings()}
	if loaded, err := store.loadSettings(); err == nil {
		store.settings = NormalizeSettings(loaded)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if account, err := store.loadAccount(); err == nil {
		store.account = &account
	} else if !errors.Is(err, os.ErrNotExist) {
		store.loadErr = err
	}
	return store, nil
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

func (s *StateStore) Account() (AccountState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.account != nil {
		return *s.account, nil
	}
	if s.loadErr != nil {
		return AccountState{}, s.loadErr
	}
	return AccountState{}, os.ErrNotExist
}

func (s *StateStore) SaveAccount(account AccountState) error {
	account.Email = strings.TrimSpace(account.Email)
	account.Password = strings.TrimSpace(account.Password)
	if account.Email == "" || account.Password == "" {
		return fmt.Errorf("email and password are required")
	}
	now := time.Now().UTC()
	if account.CreatedAt.IsZero() {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	payload, err := json.Marshal(struct {
		Version int          `json:"version"`
		Account AccountState `json:"account"`
	}{Version: stateVersion, Account: account})
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := s.aead.Seal(nonce, nonce, payload, []byte("net-games-account"))
	if err := atomicWriteFile(filepath.Join(s.dir, accountFile), sealed, 0o600); err != nil {
		return err
	}
	s.mu.Lock()
	s.account = &account
	s.loadErr = nil
	s.mu.Unlock()
	return nil
}

func (s *StateStore) DeleteAccount() error {
	if err := os.Remove(filepath.Join(s.dir, accountFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.mu.Lock()
	s.account = nil
	s.loadErr = nil
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

func (s *StateStore) loadAccount() (AccountState, error) {
	contents, err := os.ReadFile(filepath.Join(s.dir, accountFile))
	if err != nil {
		return AccountState{}, err
	}
	if len(contents) < s.aead.NonceSize() {
		return AccountState{}, fmt.Errorf("invalid account state")
	}
	plain, err := s.aead.Open(nil, contents[:s.aead.NonceSize()], contents[s.aead.NonceSize():], []byte("net-games-account"))
	if err != nil {
		return AccountState{}, err
	}
	var payload struct {
		Version int          `json:"version"`
		Account AccountState `json:"account"`
	}
	if err := json.Unmarshal(plain, &payload); err != nil {
		return AccountState{}, err
	}
	return payload.Account, nil
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
