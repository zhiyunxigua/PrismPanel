package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

func inspectArtifact(pluginID string, artifactID int64, artifactDir string, previous Manifest) (Manifest, error) {
	pluginType := normalizePluginType([]string{previous.PluginType})
	uploader := previous.UploadedBy
	if uploader.Username == "" {
		uploader = repositoryScanner
	}
	uploadedAt := previous.UploadedAt
	if uploadedAt.IsZero() {
		if info, err := os.Stat(artifactDir); err == nil {
			uploadedAt = info.ModTime().UTC()
		}
	}
	configDirectory := previous.Config.Directory
	if !validConfigDirectory(configDirectory) {
		configDirectory = previous.Name
	}
	config := ConfigSnapshot{Directory: configDirectory}
	configPath := filepath.Join(artifactDir, "config")
	if configInfo, statErr := os.Stat(configPath); statErr == nil && configInfo.IsDir() {
		var err error
		config.SHA256, config.Files, config.Size, err = treeHash(configPath)
		if err != nil {
			return Manifest{}, err
		}
		config.Present = config.Files > 0
		config.Inherited = previous.Config.Inherited && config.Present
	}
	content, err := inspectContentSnapshot(artifactDir, previous.Content)
	if err != nil {
		return Manifest{}, err
	}

	jarContents, info, jarErr := readFileLimited(filepath.Join(artifactDir, "plugin.jar"), maxPluginJARSize)
	if jarErr == nil {
		descriptors, primary, err := ParseJAR(jarContents, pluginType)
		if err != nil {
			return Manifest{}, err
		}
		hash := sha256.Sum256(jarContents)
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
			Website: primary.Website, ModMetadata: primary.ModMetadata, Descriptors: descriptors,
			Artifact: ArtifactFile{File: "plugin.jar", OriginalFilename: originalFilename,
				SHA256: hex.EncodeToString(hash[:]), Size: info.Size()},
			Config: config, Content: content, UploadedBy: uploader, UploadedAt: uploadedAt,
		}, nil
	}
	if !errors.Is(jarErr, os.ErrNotExist) {
		return Manifest{}, jarErr
	}
	// 仅内容包制品（无 plugin.jar）：身份来自已存 manifest，内容快照来自 content/ 目录。
	if previous.Name == "" || previous.Version == "" || content == nil {
		return Manifest{}, fmt.Errorf("artifact %s/%d has no jar and no stored content identity", pluginID, artifactID)
	}
	return Manifest{
		SchemaVersion: manifestSchemaVersion, ArtifactID: artifactID, PluginID: pluginID,
		PluginType: pluginType, Name: previous.Name, Version: previous.Version, Main: previous.Main,
		Authors: append([]string(nil), previous.Authors...), Description: previous.Description,
		Website: previous.Website, ModMetadata: previous.ModMetadata, Descriptors: previous.Descriptors,
		Config: config, Content: content, UploadedBy: uploader, UploadedAt: uploadedAt,
	}, nil
}

// inspectContentSnapshot 从 artifactDir/content/<id>/ 重建内容包快照：
// 以磁盘版本目录为准重建每个版本，类型沿用内容索引或 previous；current 取索引 Current，
// 失效时回退到最高版本。兼容旧版未版本化的 content/ 目录（视为版本 1）。
func inspectContentSnapshot(artifactDir string, previous *ContentSnapshot) (*ContentSnapshot, error) {
	contentRoot := filepath.Join(artifactDir, "content")
	index, err := loadContentIndex(artifactDir)
	if err != nil {
		return nil, err
	}
	storedTypes := make(map[int64]string, len(index.Versions))
	for _, version := range index.Versions {
		storedTypes[version.ContentID] = version.Type
	}
	entries, err := os.ReadDir(contentRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	versions := make([]ContentSnapshot, 0, len(entries))
	seen := make(map[int64]bool)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id, parseErr := strconv.ParseInt(entry.Name(), 10, 64)
		if parseErr != nil || id < 1 {
			continue
		}
		version := ContentSnapshot{ContentID: id, Present: true}
		version.Type = storedTypes[id]
		if version.Type == "" && previous != nil && previous.ContentID == id {
			version.Type = previous.Type
		}
		if version.Type == "" {
			continue
		}
		versionRoot := filepath.Join(contentRoot, entry.Name())
		version.SHA256, version.Files, version.Size, err = treeHash(versionRoot)
		if err != nil {
			return nil, err
		}
		if version.Files == 0 {
			continue
		}
		version.Tree, err = contentTopLevelTree(versionRoot)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
		seen[id] = true
	}
	if len(versions) == 0 {
		// 旧版未版本化 content/：直接含文件的目录视为单个版本。
		if info, statErr := os.Stat(contentRoot); statErr == nil && info.IsDir() {
			legacy := ContentSnapshot{ContentID: 1, Present: true}
			if previous != nil {
				legacy.Type = previous.Type
			}
			if legacy.Type == "" {
				legacy.Type = ContentTypeConfig
			}
			legacy.SHA256, legacy.Files, legacy.Size, err = treeHash(contentRoot)
			if err != nil {
				return nil, err
			}
			if legacy.Files == 0 {
				return nil, nil
			}
			legacy.Tree, err = contentTopLevelTree(contentRoot)
			if err != nil {
				return nil, err
			}
			return &legacy, nil
		}
		return nil, nil
	}
	sort.Slice(versions, func(left, right int) bool { return versions[left].ContentID < versions[right].ContentID })
	current := index.Current
	if current == 0 || !seen[current] {
		current = versions[len(versions)-1].ContentID
	}
	for _, version := range versions {
		if version.ContentID == current {
			result := version
			return &result, nil
		}
	}
	return nil, nil
}

func artifactMatches(stored, observed Manifest) bool {
	return stored.SchemaVersion == manifestSchemaVersion && stored.ArtifactID == observed.ArtifactID &&
		stored.PluginID == observed.PluginID && stored.PluginType == observed.PluginType &&
		stored.Name == observed.Name && stored.Version == observed.Version &&
		stored.Main == observed.Main && stored.Artifact.SHA256 == observed.Artifact.SHA256 &&
		stored.Artifact.Size == observed.Artifact.Size && stored.Config.SHA256 == observed.Config.SHA256 &&
		stored.Config.Directory == observed.Config.Directory && stored.Config.Present == observed.Config.Present &&
		storedContentMatches(stored.Content, observed.Content)
}

func storedContentMatches(stored, observed *ContentSnapshot) bool {
	if stored == nil || observed == nil {
		return stored == nil && observed == nil
	}
	return stored.Type == observed.Type && stored.SHA256 == observed.SHA256 && stored.Files == observed.Files
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
	if _, err := os.Stat(filepath.Join(sourceDir, "plugin.jar")); err == nil {
		if _, err := copyRegularFile(filepath.Join(sourceDir, "plugin.jar"), filepath.Join(staging, "plugin.jar")); err != nil {
			return Manifest{}, 0, err
		}
	}
	if observed.Config.Present {
		if _, _, err := copyTree(filepath.Join(sourceDir, "config"), filepath.Join(staging, "config")); err != nil {
			return Manifest{}, 0, err
		}
	}
	if observed.Content != nil && observed.Content.Present {
		if _, _, err := copyTree(filepath.Join(sourceDir, "content"), filepath.Join(staging, "content")); err != nil {
			return Manifest{}, 0, err
		}
		// 保留内容包版本索引（content.yaml），恢复后仍可列出多版本。
		if _, err := os.Stat(contentIndexPath(sourceDir)); err == nil {
			if _, err := copyRegularFile(contentIndexPath(sourceDir), contentIndexPath(staging)); err != nil {
				return Manifest{}, 0, err
			}
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
