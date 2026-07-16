package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	manifestSchemaVersion = 1
	maxPluginJARSize      = 256 * 1024 * 1024
)

var (
	pluginIDPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	configDirectoryPattern = regexp.MustCompile(`^[^<>:"/\\|?*\x00-\x1f]{1,100}$`)
)

type Repository struct {
	root string
	mu   sync.RWMutex
}

func NewRepository(root string) (*Repository, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		root = absolute
	}
	if err := os.MkdirAll(filepath.Join(root, "import"), 0o750); err != nil {
		return nil, fmt.Errorf("create plugin repository: %w", err)
	}
	return &Repository{root: root}, nil
}

func (r *Repository) Root() string { return r.root }

func (r *Repository) Upload(input UploadInput) (UploadResult, error) {
	if len(input.JAR) == 0 || len(input.JAR) > maxPluginJARSize {
		return UploadResult{}, fmt.Errorf("plugin jar must be between 1 byte and %d bytes", maxPluginJARSize)
	}
	descriptors, primary, err := ParseJAR(input.JAR)
	if err != nil {
		return UploadResult{}, err
	}
	if strings.TrimSpace(input.JARFilename) == "" {
		input.JARFilename = primary.Name + "-" + primary.Version + ".jar"
	}
	input.JARFilename = filepath.Base(input.JARFilename)
	if !strings.HasSuffix(strings.ToLower(input.JARFilename), ".jar") {
		return UploadResult{}, errors.New("plugin upload must use a .jar filename")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	pluginID, err := r.resolvePluginIDLocked(primary.Name)
	if err != nil {
		return UploadResult{}, err
	}
	pluginDir := filepath.Join(r.root, pluginID)
	index, _ := r.loadIndexLocked(pluginDir)
	if index.PluginID == "" {
		index = Index{SchemaVersion: 1, PluginID: pluginID, Name: primary.Name, NextArtifactID: 1}
	}
	if !strings.EqualFold(index.Name, primary.Name) {
		return UploadResult{}, errors.New("plugin id is already used by another plugin name")
	}
	current, _ := r.loadManifestLocked(pluginDir, index.CurrentArtifactID)
	configDirectory := strings.TrimSpace(input.ConfigDirectory)
	if configDirectory == "" && current.Config.Directory != "" {
		configDirectory = current.Config.Directory
	}
	if configDirectory == "" {
		configDirectory = primary.Name
	}
	if !validConfigDirectory(configDirectory) {
		return UploadResult{}, errors.New("config directory must be a single valid directory name")
	}

	if err := os.MkdirAll(pluginDir, 0o750); err != nil {
		return UploadResult{}, err
	}
	staging, err := os.MkdirTemp(pluginDir, ".artifact-*")
	if err != nil {
		return UploadResult{}, err
	}
	defer os.RemoveAll(staging)
	jarPath := filepath.Join(staging, "plugin.jar")
	if err := os.WriteFile(jarPath, input.JAR, 0o640); err != nil {
		return UploadResult{}, err
	}
	jarHash := sha256.Sum256(input.JAR)
	config := ConfigSnapshot{Directory: configDirectory}
	configPath := filepath.Join(staging, "config")
	if len(input.ConfigZIP) > 0 {
		if err := os.MkdirAll(configPath, 0o750); err != nil {
			return UploadResult{}, err
		}
		if _, _, err := extractConfigZIP(input.ConfigZIP, configPath); err != nil {
			return UploadResult{}, err
		}
		config.Present = true
	} else if current.Config.Present {
		source := filepath.Join(pluginDir, strconv.FormatInt(current.ArtifactID, 10), "config")
		if err := os.MkdirAll(configPath, 0o750); err != nil {
			return UploadResult{}, err
		}
		if _, _, err := copyTree(source, configPath); err != nil {
			return UploadResult{}, fmt.Errorf("inherit previous config: %w", err)
		}
		config.Present = true
		config.Inherited = true
	}
	if config.Present {
		config.SHA256, config.Files, config.Size, err = treeHash(configPath)
		if err != nil {
			return UploadResult{}, err
		}
		if config.Files == 0 {
			config.Present = false
			config.Inherited = false
			_ = os.RemoveAll(configPath)
		}
	}

	artifacts, err := r.loadArtifactsLocked(pluginDir)
	if err != nil {
		return UploadResult{}, err
	}
	jarHashText := hex.EncodeToString(jarHash[:])
	for _, artifact := range artifacts {
		if artifact.Artifact.SHA256 == jarHashText && artifact.Config.SHA256 == config.SHA256 &&
			artifact.Config.Directory == config.Directory {
			return UploadResult{Plugin: buildPlugin(index, artifacts), Artifact: artifact, Duplicate: true}, nil
		}
	}

	artifactID := index.NextArtifactID
	minimumArtifactID := nextArtifactID(artifacts)
	if artifactID < minimumArtifactID {
		artifactID = minimumArtifactID
	}
	manifest := Manifest{
		SchemaVersion: manifestSchemaVersion, ArtifactID: artifactID,
		PluginID: pluginID, Name: primary.Name, Version: primary.Version,
		Main: primary.Main, Authors: append([]string(nil), primary.Authors...),
		Description: primary.Description, Website: primary.Website, Descriptors: descriptors,
		Artifact: ArtifactFile{
			File: "plugin.jar", OriginalFilename: input.JARFilename,
			SHA256: jarHashText, Size: int64(len(input.JAR)),
		},
		Config: config, UploadedBy: input.Uploader, UploadedAt: time.Now().UTC(),
	}
	if manifest.Main == "" {
		manifest.Main = primary.Bootstrapper
	}
	if err := atomicYAML(filepath.Join(staging, "manifest.yaml"), manifest); err != nil {
		return UploadResult{}, err
	}
	finalDir := filepath.Join(pluginDir, strconv.FormatInt(artifactID, 10))
	if err := os.Rename(staging, finalDir); err != nil {
		return UploadResult{}, fmt.Errorf("publish plugin artifact: %w", err)
	}
	index.Name = primary.Name
	index.CurrentArtifactID = artifactID
	index.NextArtifactID = artifactID + 1
	if err := atomicYAML(filepath.Join(pluginDir, "index.yaml"), index); err != nil {
		return UploadResult{}, err
	}
	artifacts = append(artifacts, manifest)
	return UploadResult{Plugin: buildPlugin(index, artifacts), Artifact: manifest}, nil
}

func (r *Repository) List() ([]Plugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return nil, err
	}
	result := make([]Plugin, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "import" || !pluginIDPattern.MatchString(entry.Name()) {
			continue
		}
		plugin, err := r.loadPluginLocked(entry.Name())
		if err == nil {
			result = append(result, plugin)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (r *Repository) Get(pluginID string) (Plugin, error) {
	if !pluginIDPattern.MatchString(pluginID) {
		return Plugin{}, errors.New("invalid plugin id")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loadPluginLocked(pluginID)
}

func (r *Repository) Artifact(pluginID string, artifactID int64) (Manifest, string, error) {
	if !pluginIDPattern.MatchString(pluginID) || artifactID < 1 {
		return Manifest{}, "", errors.New("invalid plugin artifact")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	pluginDir := filepath.Join(r.root, pluginID)
	manifest, err := r.loadManifestLocked(pluginDir, artifactID)
	if err != nil {
		return Manifest{}, "", err
	}
	return manifest, filepath.Join(pluginDir, strconv.FormatInt(artifactID, 10)), nil
}

func (r *Repository) loadPluginLocked(pluginID string) (Plugin, error) {
	pluginDir := filepath.Join(r.root, pluginID)
	index, err := r.loadIndexLocked(pluginDir)
	if err != nil {
		return Plugin{}, err
	}
	artifacts, err := r.loadArtifactsLocked(pluginDir)
	if err != nil {
		return Plugin{}, err
	}
	if len(artifacts) == 0 {
		return Plugin{}, os.ErrNotExist
	}
	return buildPlugin(index, artifacts), nil
}

func (r *Repository) loadIndexLocked(pluginDir string) (Index, error) {
	var index Index
	err := readYAML(filepath.Join(pluginDir, "index.yaml"), &index)
	return index, err
}

func (r *Repository) loadManifestLocked(pluginDir string, artifactID int64) (Manifest, error) {
	var manifest Manifest
	if artifactID < 1 {
		return manifest, os.ErrNotExist
	}
	err := readYAML(filepath.Join(pluginDir, strconv.FormatInt(artifactID, 10), "manifest.yaml"), &manifest)
	return manifest, err
}

func (r *Repository) loadArtifactsLocked(pluginDir string) ([]Manifest, error) {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Manifest{}, nil
		}
		return nil, err
	}
	artifacts := make([]Manifest, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id, err := strconv.ParseInt(entry.Name(), 10, 64)
		if err != nil || id < 1 {
			continue
		}
		manifest, err := r.loadManifestLocked(pluginDir, id)
		if err == nil && manifest.ArtifactID == id {
			artifacts = append(artifacts, manifest)
		}
	}
	sort.Slice(artifacts, func(left, right int) bool { return artifacts[left].ArtifactID > artifacts[right].ArtifactID })
	return artifacts, nil
}

func (r *Repository) resolvePluginIDLocked(name string) (string, error) {
	base := normalizePluginID(name)
	for index := 0; index < 100; index++ {
		candidate := base
		if index > 0 {
			hash := sha256.Sum256([]byte(strings.ToLower(name)))
			candidate = fmt.Sprintf("%s-%s-%d", base, hex.EncodeToString(hash[:3]), index)
		}
		if len(candidate) > 64 {
			candidate = candidate[:64]
		}
		pluginDir := filepath.Join(r.root, candidate)
		stored, err := r.loadIndexLocked(pluginDir)
		if errors.Is(err, os.ErrNotExist) || stored.PluginID == "" {
			return candidate, nil
		}
		if err == nil && strings.EqualFold(stored.Name, name) {
			return candidate, nil
		}
	}
	return "", errors.New("cannot allocate plugin id")
}

func normalizePluginID(name string) string {
	var builder strings.Builder
	separator := false
	for _, char := range strings.ToLower(name) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			builder.WriteRune(char)
			separator = false
		} else if builder.Len() > 0 && !separator {
			builder.WriteByte('-')
			separator = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		hash := sha256.Sum256([]byte(name))
		result = "plugin-" + hex.EncodeToString(hash[:6])
	}
	if len(result) > 48 {
		result = strings.TrimRight(result[:48], "-")
	}
	return result
}

func validConfigDirectory(value string) bool {
	return value != "." && value != ".." && configDirectoryPattern.MatchString(value)
}

func nextArtifactID(artifacts []Manifest) int64 {
	var highest int64
	for _, artifact := range artifacts {
		if artifact.ArtifactID > highest {
			highest = artifact.ArtifactID
		}
	}
	return highest + 1
}

func buildPlugin(index Index, artifacts []Manifest) Plugin {
	return Plugin{PluginID: index.PluginID, Name: index.Name,
		CurrentArtifactID: index.CurrentArtifactID, Artifacts: artifacts}
}
