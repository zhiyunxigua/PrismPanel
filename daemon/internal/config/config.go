package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"PrismPanel-daemon/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	SSL     SSLConfig     `yaml:"ssl"`
	Storage StorageConfig `yaml:"storage"`
	Files   FilesConfig   `yaml:"files"`
	Process ProcessConfig `yaml:"process"`
}

type ServerConfig struct {
	Listen    string `yaml:"listen"`
	Port      int    `yaml:"port"`
	PublicURL string `yaml:"public_url"`
}

type SSLConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type StorageConfig struct {
	DataDir string `yaml:"data_dir"`
}

type FilesConfig struct {
	MaxEditFileSize int64 `yaml:"max_edit_file_size"`
	CopyConcurrency int   `yaml:"copy_concurrency"`
}

type ProcessConfig struct {
	ConsoleBufferLines int `yaml:"console_buffer_lines"`
	ShutdownTimeoutSec int `yaml:"shutdown_timeout_seconds"`
}

func Default() Config {
	return Config{
		Server:  ServerConfig{Listen: "0.0.0.0", Port: 24444},
		Storage: StorageConfig{DataDir: "data"},
		Files: FilesConfig{
			MaxEditFileSize: 5 * 1024 * 1024,
			CopyConcurrency: 4,
		},
		Process: ProcessConfig{
			ConsoleBufferLines: 2000,
			ShutdownTimeoutSec: 90,
		},
	}
}

func LoadOrCreate(path string) (Config, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		if err := save(path, cfg); err != nil {
			return Config{}, false, err
		}
		return cfg, true, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("read daemon config: %w", err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(contents, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("decode daemon config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, false, err
	}
	return cfg, false, nil
}

func save(path string, cfg Config) error {
	contents, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode daemon config: %w", err)
	}
	if err := atomicfile.WriteFile(path, contents, 0o640); err != nil {
		return fmt.Errorf("write daemon config: %w", err)
	}
	return nil
}

func (c Config) Validate() error {
	if c.Server.Listen == "" {
		return errors.New("server.listen cannot be empty")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return errors.New("server.port must be between 1 and 65535")
	}
	if c.Server.PublicURL != "" {
		u, err := url.Parse(c.Server.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("server.public_url must be an http or https URL")
		}
		if c.SSL.Enabled != (u.Scheme == "https") {
			return errors.New("server.public_url scheme does not match ssl.enabled")
		}
	}
	if c.SSL.Enabled && (c.SSL.CertFile == "" || c.SSL.KeyFile == "") {
		return errors.New("ssl.cert_file and ssl.key_file are required when SSL is enabled")
	}
	if c.Storage.DataDir == "" {
		return errors.New("storage.data_dir cannot be empty")
	}
	if c.Files.MaxEditFileSize <= 0 {
		return errors.New("files.max_edit_file_size must be positive")
	}
	if c.Files.CopyConcurrency < 1 || c.Files.CopyConcurrency > 64 {
		return errors.New("files.copy_concurrency must be between 1 and 64")
	}
	if c.Process.ConsoleBufferLines <= 0 {
		return errors.New("process.console_buffer_lines must be positive")
	}
	if c.Process.ShutdownTimeoutSec <= 0 {
		return errors.New("process.shutdown_timeout_seconds must be positive")
	}
	return nil
}

func (c Config) DataDir() string {
	if filepath.IsAbs(c.Storage.DataDir) {
		return filepath.Clean(c.Storage.DataDir)
	}
	absolute, err := filepath.Abs(c.Storage.DataDir)
	if err == nil {
		return absolute
	}
	return filepath.Clean(c.Storage.DataDir)
}
