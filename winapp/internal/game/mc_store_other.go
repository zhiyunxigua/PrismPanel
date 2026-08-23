//go:build !windows

package game

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type mcLocalStore struct{}

func newMCLocalStore() MCLocalStore { return mcLocalStore{} }

func mcStorePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "PrismPanel", "mc-account.json"), nil
}

func (mcLocalStore) Load() (MCAccount, error) {
	path, err := mcStorePath()
	if err != nil {
		return MCAccount{}, err
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return MCAccount{}, ErrMCNone
	}
	if err != nil {
		return MCAccount{}, err
	}
	var account MCAccount
	if err := json.Unmarshal(contents, &account); err != nil {
		return MCAccount{}, err
	}
	return account, nil
}

func (mcLocalStore) Save(account MCAccount) error {
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = time.Now().UTC()
	}
	path, err := mcStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	contents, err := json.MarshalIndent(account, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0o600)
}

func (mcLocalStore) Delete() error {
	path, err := mcStorePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
