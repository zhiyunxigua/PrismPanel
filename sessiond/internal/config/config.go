package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen                  string `yaml:"listen"`
	StateDir                 string `yaml:"state_dir"`
	TokenFile                string `yaml:"token_file"`
	OrphanTimeoutSeconds     int    `yaml:"orphan_timeout_seconds"`
	Token                    string `yaml:"-"`
}

func Default() Config {
	if runtime.GOOS == "windows" {
		root := filepath.Join(os.Getenv("ProgramData"), "PrismPanel", "sessiond")
		return Config{
			Listen: filepath.Join(root, "session.sock"),
			StateDir: filepath.Join(root, "state"),
			TokenFile: filepath.Join(root, "token"),
			OrphanTimeoutSeconds: 180,
		}
	}
	return Config{
		Listen: "/run/prism-sessiond/session.sock",
		StateDir: "/var/lib/prism-sessiond",
		TokenFile: "/etc/prism-sessiond/token",
		OrphanTimeoutSeconds: 180,
	}
}

func LoadOrCreate(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = defaultConfigPath()
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := save(path, cfg); err != nil {
			return Config{}, err
		}
	} else if err != nil {
		return Config{}, err
	} else if err := yaml.Unmarshal(contents, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	token, err := loadOrCreateToken(cfg.TokenFile)
	if err != nil {
		return Config{}, err
	}
	cfg.Token = token
	return cfg, nil
}

func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "PrismPanel", "sessiond", "sessiond.yaml")
	}
	return "/etc/prism-sessiond/sessiond.yaml"
}

func save(path string, cfg Config) error {
	payload, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o640)
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen cannot be empty")
	}
	if strings.TrimSpace(c.StateDir) == "" {
		return errors.New("state_dir cannot be empty")
	}
	if strings.TrimSpace(c.TokenFile) == "" {
		return errors.New("token_file cannot be empty")
	}
	if c.OrphanTimeoutSeconds <= 0 {
		c.OrphanTimeoutSeconds = 180
	}
	return nil
}

func loadOrCreateToken(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(contents))
		if token == "" {
			return "", fmt.Errorf("session token file %s is empty", path)
		}
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buffer)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}
