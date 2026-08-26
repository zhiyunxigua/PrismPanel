package plugins

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func (r *Repository) BuildBundle(pluginID string, artifactID int64, pluginTypes ...string) (string, Manifest, error) {
	return r.buildBundle(pluginID, artifactID, "plugin", pluginTypes...)
}

func (r *Repository) BuildConfigBundle(pluginID string, artifactID int64, pluginTypes ...string) (string, Manifest, error) {
	return r.buildBundle(pluginID, artifactID, "config", pluginTypes...)
}

// BuildContentBundle 打通用内容包 bundle（kind = "content"）：
// zip 内文件按相对工作目录结构打包（config/ → config/、plugins/x/config.json → plugins/x/config.json，
// 不再硬编码 plugins/<dir> 前缀）；kind 取 "config"（单独配置）或 "full"（完全配置），
// 与 Manifest.Content.Type 一致。
func (r *Repository) BuildContentBundle(pluginID string, artifactID int64, kind string, pluginTypes ...string) (string, Manifest, error) {
	return r.buildBundle(pluginID, artifactID, "content:"+kind, pluginTypes...)
}

func (r *Repository) buildBundle(pluginID string, artifactID int64, kind string, pluginTypes ...string) (string, Manifest, error) {
	manifest, artifactDir, err := r.Artifact(pluginID, artifactID, pluginTypes...)
	if err != nil {
		return "", Manifest{}, err
	}
	var contentKind string
	switch {
	case kind == "config":
		if !manifest.Config.Present {
			return "", Manifest{}, os.ErrNotExist
		}
	case kind == "plugin":
		// plugin bundle：jar 必须存在。
		if _, err := os.Stat(filepath.Join(artifactDir, "plugin.jar")); err != nil {
			return "", Manifest{}, err
		}
	case strings.HasPrefix(kind, "content:"):
		contentKind = strings.TrimPrefix(kind, "content:")
		if manifest.Content == nil || !manifest.Content.Present || manifest.Content.Type != contentKind {
			return "", Manifest{}, os.ErrNotExist
		}
	default:
		return "", Manifest{}, errors.New("unknown bundle kind")
	}
	temp, err := os.CreateTemp("", "prism-plugin-*.zip")
	if err != nil {
		return "", Manifest{}, err
	}
	path := temp.Name()
	archive := zip.NewWriter(temp)
	writeErr := addBundleManifest(archive, manifest, kind, contentKind)
	if writeErr == nil && kind == "plugin" {
		writeErr = addBundleFile(archive, filepath.Join(artifactDir, "plugin.jar"), "plugin.jar")
	}
	if writeErr == nil && kind == "config" {
		configRoot := filepath.Join(artifactDir, "config")
		writeErr = filepath.WalkDir(configRoot, func(filePath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			relative, err := filepath.Rel(configRoot, filePath)
			if err != nil {
				return err
			}
			return addBundleFile(archive, filePath, "config/"+filepath.ToSlash(relative))
		})
	}
	if writeErr == nil && contentKind != "" {
		contentRoot, err := currentContentDir(artifactDir, manifest.Content)
		if err != nil {
			writeErr = err
		} else {
			writeErr = filepath.WalkDir(contentRoot, func(filePath string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil || entry.IsDir() {
					return walkErr
				}
				relative, err := filepath.Rel(contentRoot, filePath)
				if err != nil {
					return err
				}
				// manifest.yaml 为保留名（bundle 的 manifest 占用 zip 根级该名）：
				// 内容包顶层若含同名文件会与 bundle manifest 重名，daemon 会拒绝（防重名冲突），
				// 因此打包时跳过，与 daemon deployContentToWorkspace 的跳过逻辑一致。
				if filepath.ToSlash(relative) == "manifest.yaml" {
					return nil
				}
				return addBundleFile(archive, filePath, filepath.ToSlash(relative))
			})
		}
	}
	if closeErr := archive.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if closeErr := temp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		os.Remove(path)
		return "", Manifest{}, writeErr
	}
	return path, manifest, nil
}

func addBundleManifest(archive *zip.Writer, manifest Manifest, kind, contentKind string) error {
	payload := struct {
		Kind       string          `yaml:"kind"`
		PluginType string          `yaml:"plugin_type"`
		Name       string          `yaml:"name"`
		Version    string          `yaml:"version"`
		Main       string          `yaml:"main"`
		Artifact   ArtifactFile    `yaml:"artifact"`
		Config     ConfigSnapshot  `yaml:"config"`
		Content    *ContentSnapshot `yaml:"content,omitempty"`
	}{
		Kind: kind, PluginType: manifest.PluginType, Name: manifest.Name, Version: manifest.Version,
		Main: manifest.Main, Artifact: manifest.Artifact, Config: manifest.Config,
	}
	if kind == "plugin" {
		payload.Config.Present = false
	}
	if contentKind != "" {
		payload.Kind = "content"
		payload.Content = manifest.Content
	}
	contents, err := yaml.Marshal(payload)
	if err != nil {
		return err
	}
	entry, err := archive.Create("manifest.yaml")
	if err != nil {
		return err
	}
	_, err = entry.Write(contents)
	return err
}

func addBundleFile(archive *zip.Writer, path, name string) error {
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	entry, err := archive.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, input)
	return err
}
