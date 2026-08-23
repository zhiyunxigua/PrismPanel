package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func inspectArtifact(pluginID string, artifactID int64, artifactDir string, previous Manifest) (Manifest, error) {
	contents, info, err := readFileLimited(filepath.Join(artifactDir, "plugin.jar"), maxPluginJARSize)
	if err != nil {
		return Manifest{}, err
	}
	pluginType := normalizePluginType([]string{previous.PluginType})
	descriptors, primary, err := ParseJAR(contents, pluginType)
	if err != nil {
		return Manifest{}, err
	}
	hash := sha256.Sum256(contents)
	configDirectory := previous.Config.Directory
	if !validConfigDirectory(configDirectory) {
		configDirectory = primary.Name
	}
	config := ConfigSnapshot{Directory: configDirectory}
	configPath := filepath.Join(artifactDir, "config")
	if configInfo, statErr := os.Stat(configPath); statErr == nil && configInfo.IsDir() {
		config.SHA256, config.Files, config.Size, err = treeHash(configPath)
		if err != nil {
			return Manifest{}, err
		}
		config.Present = config.Files > 0
		config.Inherited = previous.Config.Inherited && config.Present
	}
	uploader := previous.UploadedBy
	if uploader.Username == "" {
		uploader = repositoryScanner
	}
	uploadedAt := previous.UploadedAt
	if uploadedAt.IsZero() {
		uploadedAt = info.ModTime().UTC()
	}
	originalFilename := previous.Artifact.OriginalFilename
	if originalFilename == "" {
		originalFilename = primary.Name + "-" + primary.Version + ".jar"
	}
	mainClass := primary.Main
	if mainClass == "" {
		mainClass = primary.Bootstrapper
	}
	return Manifest{
		SchemaVersion: manifestSchemaVersion, ArtifactID: artifactID, PluginID: pluginID,
		PluginType: pluginType, Name: primary.Name, Version: primary.Version, Main: mainClass,
		Authors: append([]string(nil), primary.Authors...), Description: primary.Description,
		Website: primary.Website, Descriptors: descriptors,
		Artifact: ArtifactFile{File: "plugin.jar", OriginalFilename: originalFilename,
			SHA256: hex.EncodeToString(hash[:]), Size: info.Size()},
		Config: config, UploadedBy: uploader, UploadedAt: uploadedAt,
	}, nil
}

func artifactMatches(stored, observed Manifest) bool {
	return stored.SchemaVersion == manifestSchemaVersion && stored.ArtifactID == observed.ArtifactID &&
		stored.PluginID == observed.PluginID && stored.PluginType == observed.PluginType &&
		stored.Name == observed.Name && stored.Version == observed.Version &&
		stored.Main == observed.Main && stored.Artifact.SHA256 == observed.Artifact.SHA256 &&
		stored.Artifact.Size == observed.Artifact.Size && stored.Config.SHA256 == observed.Config.SHA256 &&
		stored.Config.Directory == observed.Config.Directory && stored.Config.Present == observed.Config.Present
}

func (r *Repository) recoverChangedArtifactLocked(pluginDir, sourceDir string, nextID int64, observed Manifest) (Manifest, int64, error) {
	for {
		if _, err := os.Stat(filepath.Join(pluginDir, strconv.FormatInt(nextID, 10))); errors.Is(err, os.ErrNotExist) {
			break
		}
		nextID++
	}
	staging, err := os.MkdirTemp(pluginDir, ".recovered-*")
	if err != nil {
		return Manifest{}, 0, err
	}
	defer os.RemoveAll(staging)
	if _, err := copyRegularFile(filepath.Join(sourceDir, "plugin.jar"), filepath.Join(staging, "plugin.jar")); err != nil {
		return Manifest{}, 0, err
	}
	if observed.Config.Present {
		if _, _, err := copyTree(filepath.Join(sourceDir, "config"), filepath.Join(staging, "config")); err != nil {
			return Manifest{}, 0, err
		}
	}
	observed.ArtifactID = nextID
	observed.UploadedBy = repositoryScanner
	observed.UploadedAt = time.Now().UTC()
	if err := atomicYAML(filepath.Join(staging, "manifest.yaml"), observed); err != nil {
		return Manifest{}, 0, err
	}
	finalDir := filepath.Join(pluginDir, strconv.FormatInt(nextID, 10))
	if err := os.Rename(staging, finalDir); err != nil {
		return Manifest{}, 0, err
	}
	stalePath := sourceDir + ".stale"
	for suffix := 1; ; suffix++ {
		if _, err := os.Stat(stalePath); errors.Is(err, os.ErrNotExist) {
			break
		}
		stalePath = fmt.Sprintf("%s.stale.%d", sourceDir, suffix)
	}
	if err := os.Rename(sourceDir, stalePath); err != nil {
		return Manifest{}, 0, fmt.Errorf("archive manually changed artifact: %w", err)
	}
	return observed, nextID, nil
}

func readFileLimited(path string, maximum int64) ([]byte, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, nil, fmt.Errorf("file size must be between 1 and %d bytes", maximum)
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(contents)) > maximum {
		return nil, nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return contents, info, nil
}

func copyRegularFile(source, destination string) (int64, error) {
	input, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return 0, err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}
