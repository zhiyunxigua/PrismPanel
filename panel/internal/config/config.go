package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig `yaml:"server"`
	Daemon   DaemonConfig `yaml:"daemon"`
	Frontend Frontend     `yaml:"frontend"`
	Audit    Audit        `yaml:"audit"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
	Port   int    `yaml:"port"`
}

type DaemonConfig struct {
	URL        string `yaml:"url"`
	Secret     string `yaml:"secret"`
	SecretFile string `yaml:"secret_file"`
}

type Frontend struct {
	Directory string `yaml:"directory"`
}

type Audit struct {
	File string `yaml:"file"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{Listen: "127.0.0.1", Port: 8080},
		Daemon: DaemonConfig{
			URL: "http://127.0.0.1:24444", SecretFile: "../daemon/data/secret.json",
		},
		Frontend: Frontend{Directory: "../frontend"},
		Audit:    Audit{File: "data/audit.jsonl"},
	}
}

func LoadOrCreate(path string) (Config, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		contents, marshalErr := yaml.Marshal(cfg)
		if marshalErr != nil {
			return Config{}, false, marshalErr
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return Config{}, false, err
		}
		if err := os.WriteFile(path, contents, 0o640); err != nil {
			return Config{}, false, err
		}
		return cfg, true, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	cfg := Default()
	if err := yaml.Unmarshal(contents, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("decode panel config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, false, err
	}
	return cfg, false, nil
}

func (c Config) Validate() error {
	if c.Server.Listen == "" || c.Server.Port < 1 || c.Server.Port > 65535 {
		return errors.New("panel listen address is invalid")
	}
	parsed, err := url.Parse(c.Daemon.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("daemon.url must be an http or https URL")
	}
	if c.Daemon.Secret == "" && c.Daemon.SecretFile == "" {
		return errors.New("daemon.secret or daemon.secret_file is required")
	}
	if c.Frontend.Directory == "" || c.Audit.File == "" {
		return errors.New("frontend.directory and audit.file are required")
	}
	return nil
}

func (c Config) DaemonSecret() (string, error) {
	if value := os.Getenv("PRISM_DAEMON_SECRET"); value != "" {
		return value, nil
	}
	if c.Daemon.Secret != "" {
		return c.Daemon.Secret, nil
	}
	contents, err := os.ReadFile(c.Daemon.SecretFile)
	if err != nil {
		return "", fmt.Errorf("read daemon secret file: %w", err)
	}
	var value struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(contents, &value); err != nil {
		return "", fmt.Errorf("decode daemon secret file: %w", err)
	}
	if value.Secret == "" {
		return "", errors.New("daemon secret file is empty")
	}
	return value.Secret, nil
}
