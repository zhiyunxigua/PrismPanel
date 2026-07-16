package plugins

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
)

func (r *Repository) BuildBundle(pluginID string, artifactID int64) (string, Manifest, error) {
	manifest, artifactDir, err := r.Artifact(pluginID, artifactID)
	if err != nil {
		return "", Manifest{}, err
	}
	temp, err := os.CreateTemp("", "prism-plugin-*.zip")
	if err != nil {
		return "", Manifest{}, err
	}
	path := temp.Name()
	archive := zip.NewWriter(temp)
	writeErr := addBundleFile(archive, filepath.Join(artifactDir, "plugin.jar"), "plugin.jar")
	if writeErr == nil {
		writeErr = addBundleFile(archive, filepath.Join(artifactDir, "manifest.yaml"), "manifest.yaml")
	}
	if writeErr == nil && manifest.Config.Present {
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
