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

const (
	maxBundleSize            = int64(800 * 1024 * 1024)
	maxExpandedContentSize   = int64(4 * 1024 * 1024 * 1024)
	maxBundleManifestSize    = int64(1 * 1024 * 1024)
	maxContentBundleEntries  = 65536
	maxRegularBundleEntries  = 4098
)

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
	Content struct {
		Type    string `yaml:"type"`
		Present bool   `yaml:"present"`
	} `yaml:"content"`
}

type preparedBundle struct {
	root       string
	jarPath    string
	configPath string
	contentPath string
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

	// 先读 manifest.yaml 确定 kind：内容包 bundle 允许任意相对结构条目，
	// 与 plugin/config bundle 的白名单校验不同。
	var manifestData []byte
	manifestEntries := 0
	for _, entry := range reader.File {
		name, cleanErr := cleanBundlePath(entry.Name)
		if cleanErr != nil {
			return nil, nil, cleanErr
		}
		if name != "manifest.yaml" {
			continue
		}
		manifestEntries++
		if manifestEntries > 1 {
			return nil, nil, errors.New("plugin bundle has multiple manifest.yaml entries")
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() ||
			entry.UncompressedSize64 == 0 || int64(entry.UncompressedSize64) > maxBundleManifestSize {
			return nil, nil, errors.New("plugin bundle manifest is invalid")
		}
		source, openErr := entry.Open()
		if openErr != nil {
			return nil, nil, openErr
		}
		manifestData, err = io.ReadAll(io.LimitReader(source, maxBundleManifestSize+1))
		source.Close()
		if err != nil || int64(len(manifestData)) > maxBundleManifestSize {
			return nil, nil, errors.New("plugin bundle manifest is invalid")
		}
	}
	if manifestEntries == 0 {
		return nil, nil, errors.New("plugin bundle has no manifest")
	}
	var manifest bundleManifest
	if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
		return nil, nil, fmt.Errorf("decode plugin bundle manifest: %w", err)
	}
	if manifest.PluginType == "" {
		manifest.PluginType = PluginTypeSpigot
	}
	if !validPluginType(manifest.PluginType) {
		return nil, nil, errors.New("plugin bundle type is invalid")
	}
	if manifest.Kind == "" {
		manifest.Kind = "plugin"
	}
	if manifest.Kind != "plugin" && manifest.Kind != "config" && manifest.Kind != "content" {
		return nil, nil, errors.New("plugin bundle kind is invalid")
	}
	if manifest.Kind == "content" {
		if manifest.Content.Type != "config" && manifest.Content.Type != "full" {
			return nil, nil, errors.New("plugin content bundle type must be config or full")
		}
		if !manifest.Content.Present || strings.TrimSpace(manifest.Name) == "" {
			return nil, nil, errors.New("plugin content bundle is invalid")
		}
	}
	if manifest.Kind == "config" && (!manifest.Config.Present || strings.TrimSpace(manifest.Name) == "") {
		return nil, nil, errors.New("plugin config bundle is invalid")
	}
	if manifest.Config.Present && !validDirectoryName(manifest.Config.Directory) {
		return nil, nil, errors.New("plugin config directory is invalid")
	}

	maxEntries := maxRegularBundleEntries
	expandedCap := maxBundleSize
	if manifest.Kind == "content" {
		maxEntries = maxContentBundleEntries
		expandedCap = maxExpandedContentSize
	}
	if len(reader.File) > maxEntries {
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
		if name == "" || entry.FileInfo().IsDir() || name == "manifest.yaml" {
			continue
		}
		if manifest.Kind != "content" {
			if name != "plugin.jar" && !strings.HasPrefix(name, "config/") {
				cleanup()
				return nil, nil, fmt.Errorf("unsupported plugin bundle entry: %s", name)
			}
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
			cleanup()
			return nil, nil, fmt.Errorf("unsupported plugin bundle file: %s", name)
		}
		total += int64(entry.UncompressedSize64)
		if total > expandedCap {
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
	var plugin FilePlugin
	if manifest.Kind == "content" {
		plugin = FilePlugin{PluginType: manifest.PluginType, Name: manifest.Name, Version: manifest.Version, Main: manifest.Main}
	} else if manifest.Kind == "config" {
		configInfo, err := os.Stat(filepath.Join(root, "config"))
		if err != nil || !configInfo.IsDir() {
			cleanup()
			return nil, nil, errors.New("plugin bundle config snapshot is missing")
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
		contentPath: root, manifest: manifest, plugin: plugin,
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
