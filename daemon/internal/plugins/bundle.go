package plugins

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxBundleSize = int64(800 * 1024 * 1024)

type bundleManifest struct {
	Kind       string `yaml:"kind"`
	PluginType string `yaml:"plugin_type"`
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Main       string `yaml:"main"`
	Artifact   struct {
		OriginalFilename string `yaml:"original_filename"`
		SHA256           string `yaml:"sha256"`
	} `yaml:"artifact"`
	Config struct {
		Directory string `yaml:"directory"`
		Present   bool   `yaml:"present"`
	} `yaml:"config"`
}

type preparedBundle struct {
	root       string
	jarPath    string
	configPath string
	manifest   bundleManifest
	plugin     FilePlugin
}

func prepareBundle(path string) (*preparedBundle, func(), error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxBundleSize {
		return nil, nil, errors.New("plugin bundle size is invalid")
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open plugin bundle: %w", err)
	}
	defer reader.Close()
	if len(reader.File) > 4098 {
		return nil, nil, errors.New("plugin bundle has too many entries")
	}
	root, err := os.MkdirTemp("", "prism-plugin-bundle-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	var total int64
	for _, entry := range reader.File {
		name, cleanErr := cleanBundlePath(entry.Name)
		if cleanErr != nil {
			cleanup()
			return nil, nil, cleanErr
		}
		if name == "" || entry.FileInfo().IsDir() {
			continue
		}
		if name != "plugin.jar" && name != "manifest.yaml" && !strings.HasPrefix(name, "config/") {
			cleanup()
			return nil, nil, fmt.Errorf("unsupported plugin bundle entry: %s", name)
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
			cleanup()
			return nil, nil, fmt.Errorf("unsupported plugin bundle file: %s", name)
		}
		total += int64(entry.UncompressedSize64)
		if total > maxBundleSize {
			cleanup()
			return nil, nil, errors.New("expanded plugin bundle is too large")
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			cleanup()
			return nil, nil, err
		}
		input, err := entry.Open()
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			input.Close()
			cleanup()
			return nil, nil, err
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, int64(entry.UncompressedSize64)+1))
		closeErr := output.Close()
		input.Close()
		if copyErr != nil || closeErr != nil || written != int64(entry.UncompressedSize64) {
			cleanup()
			return nil, nil, fmt.Errorf("extract plugin bundle entry: %s", name)
		}
	}
	jarPath := filepath.Join(root, "plugin.jar")
	manifestPath := filepath.Join(root, "manifest.yaml")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		cleanup()
		return nil, nil, errors.New("plugin bundle has no manifest")
	}
	var manifest bundleManifest
	if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("decode plugin bundle manifest: %w", err)
	}
	if manifest.PluginType == "" {
		manifest.PluginType = PluginTypeSpigot
	}
	if !validPluginType(manifest.PluginType) {
		cleanup()
		return nil, nil, errors.New("plugin bundle type is invalid")
	}
	if manifest.Kind == "" {
		manifest.Kind = "plugin"
	}
	if manifest.Kind != "plugin" && manifest.Kind != "config" {
		cleanup()
		return nil, nil, errors.New("plugin bundle kind is invalid")
	}
	if manifest.Config.Present && !validDirectoryName(manifest.Config.Directory) {
		cleanup()
		return nil, nil, errors.New("plugin config directory is invalid")
	}
	if manifest.Config.Present {
		if info, err := os.Stat(filepath.Join(root, "config")); err != nil || !info.IsDir() {
			cleanup()
			return nil, nil, errors.New("plugin bundle config snapshot is missing")
		}
	}
	var plugin FilePlugin
	if manifest.Kind == "config" {
		if !manifest.Config.Present || strings.TrimSpace(manifest.Name) == "" {
			cleanup()
			return nil, nil, errors.New("plugin config bundle is invalid")
		}
		plugin = FilePlugin{PluginType: manifest.PluginType, Name: manifest.Name, Version: manifest.Version, Main: manifest.Main}
	} else {
		jarInfo, err := os.Stat(jarPath)
		if err != nil {
			cleanup()
			return nil, nil, errors.New("plugin bundle has no jar")
		}
		plugin, err = scanFile(jarPath, "plugin.jar", true, jarInfo, manifest.PluginType)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if !strings.EqualFold(manifest.Name, plugin.Name) || manifest.Version != plugin.Version ||
			manifest.Artifact.SHA256 != plugin.SHA256 {
			cleanup()
			return nil, nil, errors.New("plugin bundle manifest does not match jar")
		}
	}
	return &preparedBundle{
		root: root, jarPath: jarPath, configPath: filepath.Join(root, "config"),
		manifest: manifest, plugin: plugin,
	}, cleanup, nil
}

func prepareUploadedJAR(path, originalFilename string, pluginTypes ...string) (*preparedBundle, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, errors.New("plugin jar is unavailable")
	}
	plugin, err := scanFile(path, originalFilename, true, info, requestedPluginType(pluginTypes))
	if err != nil {
		return nil, err
	}
	var manifest bundleManifest
	manifest.PluginType = plugin.PluginType
	manifest.Name = plugin.Name
	manifest.Version = plugin.Version
	manifest.Main = plugin.Main
	manifest.Artifact.OriginalFilename = originalFilename
	manifest.Artifact.SHA256 = plugin.SHA256
	return &preparedBundle{
		jarPath: path, manifest: manifest, plugin: plugin,
	}, nil
}

func cleanBundlePath(value string) (string, error) {
	value = strings.TrimPrefix(strings.ReplaceAll(value, "\\", "/"), "/")
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." {
		return "", nil
	}
	if filepath.IsAbs(filepath.FromSlash(value)) || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(clean, 0) {
		return "", errors.New("plugin bundle path escapes root")
	}
	return clean, nil
}

func validDirectoryName(value string) bool {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value {
		return false
	}
	for _, char := range value {
		switch char {
		case '<', '>', ':', 34, '/', 92, '|', '?', '*':
			return false
		}
		if char < 32 {
			return false
		}
	}
	return true
}

func fileSHA256(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
