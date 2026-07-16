package model

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const SchemaVersion = 1

var serverIDPattern = regexp.MustCompile("^[a-z0-9_-]+$")

type ServerConfig struct {
	SchemaVersion  int            `json:"schema_version"`
	Type           string         `json:"type"`
	ServerID       string         `json:"server_id"`
	Name           string         `json:"name"`
	Workspace      string         `json:"workspace,omitempty"`
	Port           int            `json:"port,omitempty"`
	RootPath       string         `json:"root_path,omitempty"`
	ImageDirectory string         `json:"image_directory,omitempty"`
	InstanceCount  int            `json:"instance_count,omitempty"`
	Ports          []int          `json:"ports,omitempty"`
	Exclude        []ExcludeEntry `json:"exclude,omitempty"`
	Process        ProcessConfig  `json:"process"`
	Console        ConsoleConfig  `json:"console"`
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
	Name       string
	Workspace  string
	Port       int
	Process    ProcessConfig
	Console    ConsoleConfig
}

func (s *ServerConfig) Normalize() {
	if s.Process.StopCommand == "" {
		s.Process.StopCommand = "stop"
	}
	if s.Process.StopTimeoutSeconds == 0 {
		s.Process.StopTimeoutSeconds = 60
	}
	s.Console.Encoding = normalizeConsoleEncoding(s.Console.Encoding)
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
	return nil
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
			InstanceID: s.ServerID, ServerID: s.ServerID, ServerType: s.Type,
			Name: s.Name, Workspace: filepath.Clean(s.Workspace), Port: s.Port,
			Process: s.Process, Console: s.Console,
		}}
	}
	instances := make([]InstanceConfig, 0, s.InstanceCount)
	for slot := 1; slot <= s.InstanceCount; slot++ {
		instanceID := fmt.Sprintf("%s_%d", s.ServerID, slot)
		instances = append(instances, InstanceConfig{
			InstanceID: instanceID, ServerID: s.ServerID, Slot: slot, ServerType: s.Type,
			Name:      fmt.Sprintf("%s #%d", s.Name, slot),
			Workspace: filepath.Join(s.RootPath, instanceID), Port: s.Ports[slot-1],
			Process: s.Process, Console: s.Console,
		})
	}
	return instances
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
