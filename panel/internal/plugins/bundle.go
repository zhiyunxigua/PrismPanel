package plugins

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func (r *Repository) BuildBundle(pluginID string, artifactID int64, pluginTypes ...string) (string, Manifest, error) {
	return r.buildBundle(pluginID, artifactID, "plugin", pluginTypes...)
}

func (r *Repository) BuildConfigBundle(pluginID string, artifactID int64, pluginTypes ...string) (string, Manifest, error) {
	return r.buildBundle(pluginID, artifactID, "config", pluginTypes...)
}

func (r *Repository) buildBundle(pluginID string, artifactID int64, kind string, pluginTypes ...string) (string, Manifest, error) {
	manifest, artifactDir, err := r.Artifact(pluginID, artifactID, pluginTypes...)
	if err != nil {
		return "", Manifest{}, err
	}
	if kind == "config" && !manifest.Config.Present {
		return "", Manifest{}, os.ErrNotExist
	}
	temp, err := os.CreateTemp("", "prism-plugin-*.zip")
	if err != nil {
		return "", Manifest{}, err
	}
	path := temp.Name()
	archive := zip.NewWriter(temp)
	writeErr := addBundleManifest(archive, manifest, kind)
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

func addBundleManifest(archive *zip.Writer, manifest Manifest, kind string) error {
	payload := struct {
		Kind       string         `yaml:"kind"`
		PluginType string         `yaml:"plugin_type"`
		Name       string         `yaml:"name"`
		Version    string         `yaml:"version"`
		Main       string         `yaml:"main"`
		Artifact   ArtifactFile   `yaml:"artifact"`
		Config     ConfigSnapshot `yaml:"config"`
	}{
		Kind: kind, PluginType: manifest.PluginType, Name: manifest.Name, Version: manifest.Version,
		Main: manifest.Main, Artifact: manifest.Artifact, Config: manifest.Config,
	}
	if kind == "plugin" {
		payload.Config.Present = false
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
