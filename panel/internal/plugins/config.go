package plugins

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

const maxConfigEditFileSize = int64(8 * 1024 * 1024)

type ConfigFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func (r *Repository) ListConfig(pluginID string, artifactID int64, pluginTypes ...string) ([]ConfigFile, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	manifest, artifactDir, err := r.artifactLocked(pluginID, artifactID, pluginTypes...)
	if err != nil || !manifest.Config.Present {
		return []ConfigFile{}, err
	}
	root := filepath.Join(artifactDir, "config")
	files := make([]ConfigFile, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("config contains unsupported entry %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, ConfigFile{Path: filepath.ToSlash(relative), Size: info.Size()})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return []ConfigFile{}, nil
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, err
}

func (r *Repository) ReadConfig(pluginID string, artifactID int64, path string, pluginTypes ...string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	manifest, artifactDir, err := r.artifactLocked(pluginID, artifactID, pluginTypes...)
	if err != nil {
		return nil, err
	}
	configPath, err := configFilePath(filepath.Join(artifactDir, "config"), path)
	if err != nil {
		return nil, err
	}
	if !manifest.Config.Present {
		return nil, errors.New("plugin artifact has no config snapshot")
	}
	info, err := os.Stat(configPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("config path is not a regular file")
	}
	if info.Size() > maxConfigEditFileSize {
		return nil, fmt.Errorf("config file exceeds %d bytes", maxConfigEditFileSize)
	}
	return os.ReadFile(configPath)
}

func (r *Repository) UpdateConfig(pluginID string, artifactID int64, path string, contents []byte, pluginTypes ...string) (Manifest, error) {
	if int64(len(contents)) > maxConfigEditFileSize {
		return Manifest{}, fmt.Errorf("config file exceeds %d bytes", maxConfigEditFileSize)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, artifactDir, err := r.artifactLocked(pluginID, artifactID, pluginTypes...)
	if err != nil {
		return Manifest{}, err
	}
	if !manifest.Config.Present {
		return Manifest{}, errors.New("plugin artifact has no config snapshot")
	}
	configPath, err := configFilePath(filepath.Join(artifactDir, "config"), path)
	if err != nil {
		return Manifest{}, err
	}
	if err := writeConfigFile(configPath, contents); err != nil {
		return Manifest{}, err
	}
	manifest.Config.SHA256, manifest.Config.Files, manifest.Config.Size, err = treeHash(filepath.Join(artifactDir, "config"))
	if err != nil {
		return Manifest{}, err
	}
	manifest.Config.Inherited = false
	if err := atomicYAML(filepath.Join(artifactDir, "manifest.yaml"), manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (r *Repository) artifactLocked(pluginID string, artifactID int64, pluginTypes ...string) (Manifest, string, error) {
	if !pluginIDPattern.MatchString(pluginID) || artifactID < 1 {
		return Manifest{}, "", errors.New("invalid plugin artifact")
	}
	pluginDir := filepath.Join(r.typeRoot(normalizePluginType(pluginTypes)), pluginID)
	manifest, err := r.loadManifestLocked(pluginDir, artifactID)
	return manifest, filepath.Join(pluginDir, strconv.FormatInt(artifactID, 10)), err
}

func configFilePath(root, value string) (string, error) {
	clean, err := cleanArchivePath(value)
	if err != nil || clean == "" {
		if err != nil {
			return "", err
		}
		return "", errors.New("config file path is required")
	}
	path := filepath.Join(root, filepath.FromSlash(clean))
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || filepath.IsAbs(relative) {
		return "", errors.New("config path escapes root")
	}
	return path, nil
}

func writeConfigFile(path string, contents []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o640); err == nil {
		_, err = temp.Write(contents)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(tempPath, path)
	}
	return nil
}
