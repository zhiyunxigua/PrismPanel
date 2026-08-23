package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"PrismPanel-daemon/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	SSL      SSLConfig      `yaml:"ssl"`
	Security SecurityConfig `yaml:"security"`
	Storage  StorageConfig  `yaml:"storage"`
	Files    FilesConfig    `yaml:"files"`
	Process  ProcessConfig  `yaml:"process"`
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

type SecurityConfig struct {
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs"`
}

type StorageConfig struct {
	DataDir string `yaml:"data_dir"`
}

type FilesConfig struct {
	MaxEditFileSize        int64 `yaml:"max_edit_file_size"`
	MaxUploadFileSize      int64 `yaml:"max_upload_file_size"`
	MaxExtractedSize       int64 `yaml:"max_extracted_size"`
	MaxArchiveDownloadSize int64 `yaml:"max_archive_download_size"`
	MaxConcurrentTransfers int   `yaml:"max_concurrent_transfers"`
	CopyConcurrency        int   `yaml:"copy_concurrency"`
}

type ProcessConfig struct {
	ConsoleBufferLines int `yaml:"console_buffer_lines"`
	ShutdownTimeoutSec int `yaml:"shutdown_timeout_seconds"`
}

func Default() Config {
	return Config{
		Server:   ServerConfig{Listen: "0.0.0.0", Port: 24444},
		Security: SecurityConfig{TrustedProxyCIDRs: []string{}},
		Storage:  StorageConfig{DataDir: "data"},
		Files: FilesConfig{
			MaxEditFileSize:        5 * 1024 * 1024,
			MaxUploadFileSize:      2 * 1024 * 1024 * 1024,
			MaxExtractedSize:       20 * 1024 * 1024 * 1024,
			MaxArchiveDownloadSize: 1 * 1024 * 1024 * 1024,
			MaxConcurrentTransfers: 4,
			CopyConcurrency:        4,
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
	}
	if c.SSL.Enabled && (c.SSL.CertFile == "" || c.SSL.KeyFile == "") {
		return errors.New("ssl.cert_file and ssl.key_file are required when SSL is enabled")
	}
	if err := c.Security.Validate(); err != nil {
		return err
	}
	if c.Storage.DataDir == "" {
		return errors.New("storage.data_dir cannot be empty")
	}
	if c.Files.MaxEditFileSize <= 0 {
		return errors.New("files.max_edit_file_size must be positive")
	}
	if c.Files.MaxUploadFileSize <= 0 {
		return errors.New("files.max_upload_file_size must be positive")
	}
	if c.Files.MaxExtractedSize <= 0 {
		return errors.New("files.max_extracted_size must be positive")
	}
	if c.Files.MaxArchiveDownloadSize <= 0 {
		return errors.New("files.max_archive_download_size must be positive")
	}
	if c.Files.MaxConcurrentTransfers < 1 || c.Files.MaxConcurrentTransfers > 64 {
		return errors.New("files.max_concurrent_transfers must be between 1 and 64")
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

func (c SecurityConfig) Validate() error {
	seen := make(map[string]struct{}, len(c.TrustedProxyCIDRs))
	for _, raw := range c.TrustedProxyCIDRs {
		prefix, err := normalizeTrustedProxyCIDR(raw)
		if err != nil {
			return fmt.Errorf("security.trusted_proxy_cidrs: %w", err)
		}
		value := prefix.String()
		if _, exists := seen[value]; exists {
			return fmt.Errorf("security.trusted_proxy_cidrs contains duplicate CIDR %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (c SecurityConfig) IsTrustedProxy(address netip.Addr) bool {
	address = address.Unmap()
	for _, raw := range c.TrustedProxyCIDRs {
		prefix, err := normalizeTrustedProxyCIDR(raw)
		if err == nil && prefix.Contains(address) {
			return true
		}
	}
	return false
}

func normalizeTrustedProxyCIDR(raw string) (netip.Prefix, error) {
	value := strings.TrimSpace(raw)
	if value == "" || value != raw {
		return netip.Prefix{}, errors.New("CIDR must not be empty or padded")
	}
	if address, err := netip.ParseAddr(value); err == nil {
		address = address.Unmap()
		return netip.PrefixFrom(address, address.BitLen()), nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || prefix.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q", raw)
	}
	return prefix.Masked(), nil
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
