package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Settings struct {
	PanelURL string `json:"panel_url"`
}

type Store struct {
	path string
}

func NewStore(path string) Store {
	return Store{path: path}
}

func DefaultPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(directory, "PrismPanel", "settings.json"), nil
}

func (s Store) Load() (Settings, error) {
	contents, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read WinApp settings: %w", err)
	}
	var value Settings
	if err := json.Unmarshal(contents, &value); err != nil {
		return Settings{}, fmt.Errorf("decode WinApp settings: %w", err)
	}
	return value, nil
}

func (s Store) Save(value Settings) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode WinApp settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create WinApp settings directory: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(s.path, contents, 0o600); err != nil {
		return fmt.Errorf("write WinApp settings: %w", err)
	}
	return nil
}
