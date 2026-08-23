package config

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Auth      AuthConfig      `yaml:"auth"`
	Security  SecurityConfig  `yaml:"security"`
	Minecraft MinecraftConfig `yaml:"minecraft"`
	Frontend  Frontend        `yaml:"frontend"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
	Port   int    `yaml:"port"`
}

type DatabaseConfig struct {
	Type         string `yaml:"type"`        // "mysql" (default) or "sqlite"
	SQLitePath   string `yaml:"sqlite_path"` // SQLite 文件路径（type=sqlite 或 MySQL 不可用回退时）
	URL          string `yaml:"url"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	Name         string `yaml:"name"`
	TablePrefix  string `yaml:"table_prefix"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type AuthConfig struct {
	CookieName      string `yaml:"cookie_name"`
	CookieSecure    bool   `yaml:"cookie_secure"`
	CookieSameSite  string `yaml:"cookie_same_site"`
	SessionLifetime string `yaml:"session_lifetime"`
	IdleTimeout     string `yaml:"idle_timeout"`
}

type SecurityConfig struct {
	MasterKeyFile     string   `yaml:"master_key_file"`
	AllowedOrigins    []string `yaml:"allowed_origins"`
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs"`
}

type MinecraftConfig struct {
	ManageOperators bool `yaml:"manage_operators"`
}

type Frontend struct {
	Directory string `yaml:"directory"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{Listen: "127.0.0.1", Port: 8080},
		Database: DatabaseConfig{
			Type:         "mysql",
			SQLitePath:   "panel.db",
			URL:          "127.0.0.1:3306",
			Username:     "root",
			Name:         "prismpanel",
			TablePrefix:  "prism_",
			MaxOpenConns: 10,
			MaxIdleConns: 5,
		},
		Auth: AuthConfig{
			CookieName: "prism_session", CookieSameSite: "lax",
			SessionLifetime: "24h", IdleTimeout: "2h",
		},
		Security: SecurityConfig{
			MasterKeyFile: "data/master.key",
			AllowedOrigins: []string{
				"wails://wails.localhost", "http://wails.localhost", "https://wails.localhost",
			},
			TrustedProxyCIDRs: []string{},
		},
		Minecraft: MinecraftConfig{ManageOperators: true},
		Frontend:  Frontend{Directory: "../frontend/dist"},
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
	if err := c.Database.Validate(); err != nil {
		return err
	}
	if c.Database.MaxOpenConns < 1 || c.Database.MaxIdleConns < 0 ||
		c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		return errors.New("database connection limits are invalid")
	}
	if strings.TrimSpace(c.Auth.CookieName) == "" || strings.ContainsAny(c.Auth.CookieName, " ;=\t\r\n") {
		return errors.New("auth.cookie_name is invalid")
	}
	if err := c.Auth.Validate(); err != nil {
		return err
	}
	if _, err := c.SessionLifetime(); err != nil {
		return err
	}
	if _, err := c.IdleTimeout(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Security.MasterKeyFile) == "" {
		return errors.New("security.master_key_file is required")
	}
	if err := c.Security.Validate(); err != nil {
		return err
	}
	if c.Frontend.Directory == "" {
		return errors.New("frontend.directory is required")
	}
	return nil
}

func (c AuthConfig) Validate() error {
	switch strings.ToLower(strings.TrimSpace(c.CookieSameSite)) {
	case "", "lax", "strict":
		return nil
	case "none":
		if !c.CookieSecure {
			return errors.New("auth.cookie_same_site none requires auth.cookie_secure")
		}
		return nil
	default:
		return errors.New("auth.cookie_same_site must be lax, strict, or none")
	}
}

func (c AuthConfig) CookieSameSiteMode() http.SameSite {
	switch strings.ToLower(strings.TrimSpace(c.CookieSameSite)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func (c SecurityConfig) Validate() error {
	seen := make(map[string]struct{}, len(c.AllowedOrigins))
	for _, raw := range c.AllowedOrigins {
		origin, err := NormalizeOrigin(raw)
		if err != nil {
			return fmt.Errorf("security.allowed_origins: %w", err)
		}
		if _, exists := seen[origin]; exists {
			return fmt.Errorf("security.allowed_origins contains duplicate origin %q", origin)
		}
		seen[origin] = struct{}{}
	}
	proxySeen := make(map[string]struct{}, len(c.TrustedProxyCIDRs))
	for _, raw := range c.TrustedProxyCIDRs {
		prefix, err := normalizeTrustedProxyCIDR(raw)
		if err != nil {
			return fmt.Errorf("security.trusted_proxy_cidrs: %w", err)
		}
		value := prefix.String()
		if _, exists := proxySeen[value]; exists {
			return fmt.Errorf("security.trusted_proxy_cidrs contains duplicate CIDR %q", value)
		}
		proxySeen[value] = struct{}{}
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

func NormalizeOrigin(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || value != raw {
		return "", errors.New("origin must not be empty or padded")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("origin %q is invalid", raw)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "wails" {
		return "", fmt.Errorf("origin %q uses unsupported scheme", raw)
	}
	return scheme + "://" + strings.ToLower(parsed.Host), nil
}

var databaseIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func (c DatabaseConfig) Validate() error {
	if c.Type != "" && !strings.EqualFold(c.Type, "mysql") && !strings.EqualFold(c.Type, "sqlite") {
		return errors.New("database.type must be mysql or sqlite")
	}
	if strings.EqualFold(c.Type, "sqlite") {
		return nil
	}
	host, portText, err := net.SplitHostPort(strings.TrimSpace(c.URL))
	if err != nil || strings.TrimSpace(host) == "" {
		return errors.New("database.url must use host:port format")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("database.url port must be between 1 and 65535")
	}
	if strings.TrimSpace(c.Username) == "" {
		return errors.New("database.username is required")
	}
	if !databaseIdentifierPattern.MatchString(c.Name) || len(c.Name) > 64 {
		return errors.New("database.name is invalid")
	}
	if !databaseIdentifierPattern.MatchString(c.TablePrefix) || len(c.TablePrefix) > 24 {
		return errors.New("database.table_prefix is invalid")
	}
	return nil
}

func (c DatabaseConfig) DSN() string {
	value := mysql.NewConfig()
	value.User = c.Username
	value.Passwd = c.Password
	value.Net = "tcp"
	value.Addr = c.URL
	value.DBName = c.Name
	value.Collation = "utf8mb4_unicode_ci"
	value.ParseTime = true
	return value.FormatDSN()
}

func (c Config) SessionLifetime() (time.Duration, error) {
	return positiveDuration("auth.session_lifetime", c.Auth.SessionLifetime)
}

func (c Config) IdleTimeout() (time.Duration, error) {
	return positiveDuration("auth.idle_timeout", c.Auth.IdleTimeout)
}

func positiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return duration, nil
}
