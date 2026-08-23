package model

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const SchemaVersion = 2

var serverIDPattern = regexp.MustCompile("^[a-z0-9_-]+$")

var DefaultPluginConfigSyncExtensions = []string{
	".yml", ".yaml", ".json", ".toml", ".ini", ".conf", ".properties", ".xml",
}

// DefaultConfigSyncDirectories 是镜像服务器组的默认配置同步根目录。
// 旧配置没有 config_sync_directories 字段时只同步 plugins，保持向后兼容。
var DefaultConfigSyncDirectories = []string{"plugins"}

// ModConfigSyncDirectories 是 mod 平台（Fabric/Forge）的默认配置同步根目录。
var ModConfigSyncDirectories = []string{"config", "plugins"}

type ServerConfig struct {
	SchemaVersion              int            `json:"schema_version"`
	Type                       string         `json:"type"`
	Platform                   string         `json:"platform"`
	ServerID                   string         `json:"server_id"`
	Name                       string         `json:"name"`
	Workspace                  string         `json:"workspace,omitempty"`
	Port                       int            `json:"port,omitempty"`
	RootPath                   string         `json:"root_path,omitempty"`
	ImageDirectory             string         `json:"image_directory,omitempty"`
	InstanceCount              int            `json:"instance_count,omitempty"`
	Ports                      []int          `json:"ports,omitempty"`
	Exclude                    []ExcludeEntry `json:"exclude,omitempty"`
	PluginConfigSyncExtensions []string       `json:"plugin_config_sync_extensions,omitempty"`
	ConfigSyncDirectories      []string       `json:"config_sync_directories,omitempty"`
	Process                    ProcessConfig  `json:"process"`
	Console                    ConsoleConfig  `json:"console"`
}

type ProcessConfig struct {
	StartCommand       string `json:"start_command"`
	StopCommand        string `json:"stop_command"`
	StopTimeoutSeconds int    `json:"stop_timeout_seconds"`
	AutoStart          bool   `json:"auto_start"`
	AutoRestart        bool   `json:"auto_restart"`
}

type ConsoleConfig struct {
	Encoding string `json:"encoding"`
}

type ExcludeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type InstanceConfig struct {
	InstanceID string
	ServerID   string
	Slot       int
	ServerType string
	Platform   string
	Name       string
	Workspace  string
	Port       int
	Process    ProcessConfig
	Console    ConsoleConfig
}

func (s *ServerConfig) Normalize() {
	if s.Platform == "" {
		s.Platform = "paper"
	}
	if s.Process.StopCommand == "" {
		s.Process.StopCommand = "stop"
	}
	if s.Process.StopTimeoutSeconds == 0 {
		s.Process.StopTimeoutSeconds = 60
	}
	s.Console.Encoding = normalizeConsoleEncoding(s.Console.Encoding)
	if s.Type == "mirror" {
		s.PluginConfigSyncExtensions = normalizePluginConfigSyncExtensions(s.PluginConfigSyncExtensions)
		s.ConfigSyncDirectories = normalizeConfigSyncDirectories(s.ConfigSyncDirectories, s.Platform)
	} else {
		s.PluginConfigSyncExtensions = nil
		s.ConfigSyncDirectories = nil
	}
}

func (s ServerConfig) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", s.SchemaVersion)
	}
	if !serverIDPattern.MatchString(s.ServerID) {
		return errors.New("server_id must contain only lowercase letters, digits, '-' and '_'")
	}
	if s.Name == "" {
		return errors.New("name cannot be empty")
	}
	if s.Type != "standalone" && s.Type != "mirror" {
		return errors.New("type must be standalone or mirror")
	}
	if !IsSupportedPlatform(s.Platform) {
		return errors.New("platform must be paper, spigot, fabric, forge, velocity or bungee")
	}
	if s.Type == "mirror" && IsProxyPlatform(s.Platform) {
		return errors.New("mirror servers only support paper, spigot, fabric or forge")
	}
	if strings.TrimSpace(s.Process.StartCommand) == "" {
		return errors.New("process.start_command cannot be empty")
	}
	if len(s.Process.StartCommand) > 32768 || strings.ContainsRune(s.Process.StartCommand, 0) {
		return errors.New("process.start_command is invalid or too long")
	}
	if strings.TrimSpace(s.Process.StopCommand) == "" {
		return errors.New("process.stop_command cannot be empty")
	}
	if len(s.Process.StopCommand) > 8192 || strings.ContainsAny(s.Process.StopCommand, "\x00\r\n") {
		return errors.New("process.stop_command is invalid or too long")
	}
	if s.Process.StopTimeoutSeconds <= 0 {
		return errors.New("process.stop_timeout_seconds must be positive")
	}
	if s.Console.Encoding != "utf-8" && s.Console.Encoding != "gbk" {
		return errors.New("console.encoding must be utf-8 or gbk")
	}
	if s.Type == "standalone" {
		if !filepath.IsAbs(s.Workspace) {
			return errors.New("workspace must be an absolute path")
		}
		return validatePort(s.Port)
	}
	if !filepath.IsAbs(s.RootPath) {
		return errors.New("root_path must be an absolute path")
	}
	if err := validateRelativePath(s.ImageDirectory); err != nil {
		return fmt.Errorf("image_directory: %w", err)
	}
	if s.InstanceCount <= 0 {
		return errors.New("instance_count must be positive")
	}
	if len(s.Ports) < s.InstanceCount {
		return errors.New("ports must contain at least instance_count entries")
	}
	for _, port := range s.Ports[:s.InstanceCount] {
		if err := validatePort(port); err != nil {
			return err
		}
	}
	for _, entry := range s.Exclude {
		if entry.Type != "file" && entry.Type != "directory" {
			return errors.New("exclude.type must be file or directory")
		}
		if err := validateRelativePath(entry.Path); err != nil {
			return fmt.Errorf("exclude path %q: %w", entry.Path, err)
		}
	}
	if s.Type == "mirror" {
		for _, extension := range s.PluginConfigSyncExtensions {
			if err := validatePluginConfigSyncExtension(extension); err != nil {
				return fmt.Errorf("plugin_config_sync_extensions: %w", err)
			}
		}
		for _, directory := range s.ConfigSyncDirectories {
			if err := validateRelativePath(directory); err != nil {
				return fmt.Errorf("config_sync_directories: %w", err)
			}
		}
		// 注意：目录存在性不做校验——配置校验仅保证路径格式合法；
		// 同步根是否真实存在于镜像源属于运行时问题，由 daemon 部署时
		// syncConfigDirectory 在 INVALID_CONFIG 错误中给出（含目录名）。
	}
	return nil
}

func normalizeConfigSyncDirectories(directories []string, platform string) []string {
	seen := make(map[string]struct{}, len(directories))
	result := make([]string, 0, len(directories))
	for _, directory := range directories {
		// 只 trim + 去重 + 空值处理，不做大小写转换：Linux 上目录名区分大小写，
		// 显式传入的 "Config" 必须原样保留，小写化会导致真实目录同步失败且无提示。
		directory = strings.TrimSpace(directory)
		if directory == "" || directory == "." {
			continue
		}
		if _, exists := seen[directory]; exists {
			continue
		}
		seen[directory] = struct{}{}
		result = append(result, directory)
	}
	if len(result) == 0 {
		if IsModPlatform(platform) {
			return append([]string(nil), ModConfigSyncDirectories...)
		}
		return append([]string(nil), DefaultConfigSyncDirectories...)
	}
	return result
}

func normalizePluginConfigSyncExtensions(extensions []string) []string {
	if len(extensions) == 0 {
		return append([]string(nil), DefaultPluginConfigSyncExtensions...)
	}
	seen := make(map[string]struct{}, len(extensions))
	result := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension == "" {
			continue
		}
		if !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		if _, exists := seen[extension]; exists {
			continue
		}
		seen[extension] = struct{}{}
		result = append(result, extension)
	}
	if len(result) == 0 {
		return append([]string(nil), DefaultPluginConfigSyncExtensions...)
	}
	return result
}

func validatePluginConfigSyncExtension(extension string) error {
	if extension == "" || extension[0] != '.' || extension == "." || extension == ".." {
		return errors.New("extension must start with a dot")
	}
	if strings.ContainsAny(extension, "/\\\x00\r\n") {
		return errors.New("extension contains invalid characters")
	}
	return nil
}

func (s ServerConfig) AllowsPluginConfigSync(name string) bool {
	extension := strings.ToLower(filepath.Ext(name))
	if extension == "" {
		return false
	}
	for _, allowed := range s.PluginConfigSyncExtensions {
		if strings.EqualFold(extension, allowed) {
			return true
		}
	}
	return false
}

func normalizeConsoleEncoding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "utf8", "utf-8":
		return "utf-8"
	case "gbk":
		return "gbk"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (s ServerConfig) Instances() []InstanceConfig {
	if s.Type == "standalone" {
		return []InstanceConfig{{
			InstanceID: s.ServerID, ServerID: s.ServerID, ServerType: s.Type, Platform: s.Platform,
			Name: s.Name, Workspace: filepath.Clean(s.Workspace), Port: s.Port,
			Process: s.Process, Console: s.Console,
		}}
	}
	instances := make([]InstanceConfig, 0, s.InstanceCount)
	for slot := 1; slot <= s.InstanceCount; slot++ {
		instanceID := fmt.Sprintf("%s_%d", s.ServerID, slot)
		instances = append(instances, InstanceConfig{
			InstanceID: instanceID, ServerID: s.ServerID, Slot: slot, ServerType: s.Type, Platform: s.Platform,
			Name:      fmt.Sprintf("%s #%d", s.Name, slot),
			Workspace: filepath.Join(s.RootPath, instanceID), Port: s.Ports[slot-1],
			Process: s.Process, Console: s.Console,
		})
	}
	return instances
}

func IsSupportedPlatform(platform string) bool {
	switch platform {
	case "paper", "spigot", "fabric", "forge", "velocity", "bungee":
		return true
	default:
		return false
	}
}

func IsProxyPlatform(platform string) bool {
	return platform == "velocity" || platform == "bungee"
}

// IsModPlatform 判断平台是否为 mod 加载器平台（Fabric/Forge）。
func IsModPlatform(platform string) bool {
	return platform == "fabric" || platform == "forge"
}

// ModTypeForPlatform 返回平台对应的 mod 加载器类型；非 mod 平台返回空字符串。
func ModTypeForPlatform(platform string) string {
	if IsModPlatform(platform) {
		return platform
	}
	return ""
}

// ArtifactDirectory 返回平台对应的可加载制品目录：mod 平台为 mods，其余为 plugins。
func ArtifactDirectory(platform string) string {
	if IsModPlatform(platform) {
		return "mods"
	}
	return "plugins"
}

// IsModArtifact 判断制品类型（fabric/forge）是否属于 mod 平台。
func IsModArtifact(artifactType string) bool {
	return artifactType == "fabric" || artifactType == "forge"
}

func PluginTypeForPlatform(platform string) string {
	if IsProxyPlatform(platform) {
		return platform
	}
	if IsModPlatform(platform) {
		return platform
	}
	return "spigot"
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is outside 1-65535", port)
	}
	return nil
}

func validateRelativePath(path string) error {
	if path == "" || filepath.IsAbs(path) {
		return errors.New("must be a non-empty relative path")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("path escapes its root")
	}
	return nil
}
