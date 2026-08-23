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
	manifestSchemaVersion = 2
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

// repositoryTypes 是插件仓库支持的制品类型（插件 + mod）。
// 顺序与前端「仓库」页 7 平台 tab 显示顺序一致：
// fabric / forge / neoforge / spigot / paper / velocity / bungee。
var repositoryTypes = []string{
	PluginTypeFabric, PluginTypeForge, PluginTypeNeoForge,
	PluginTypeSpigot, PluginTypePaper, PluginTypeVelocity, PluginTypeBungee,
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
	for _, pluginType := range repositoryTypes {
		if err := os.MkdirAll(filepath.Join(root, pluginType, "import"), 0o750); err != nil {
			return nil, fmt.Errorf("create plugin repository: %w", err)
		}
	}
	return &Repository{root: root}, nil
}

func (r *Repository) Root() string { return r.typeRoot(PluginTypeSpigot) }

func (r *Repository) typeRoot(pluginType string) string {
	return filepath.Join(r.root, pluginType)
}

func (r *Repository) Upload(input UploadInput) (UploadResult, error) {
	input.PluginType = normalizePluginType([]string{input.PluginType})
	if !ValidPluginType(input.PluginType) {
		return UploadResult{}, errors.New("plugin type must be spigot, paper, velocity, bungee, fabric, forge or neoforge")
	}
	hasJAR := len(input.JAR) > 0
	hasContent := len(input.ContentZIP) > 0
	hasLegacyConfig := len(input.ConfigZIP) > 0
	if !hasJAR && !hasContent {
		return UploadResult{}, errors.New("plugin upload requires a jar or a content bundle")
	}
	if hasJAR && len(input.JAR) > maxPluginJARSize {
		return UploadResult{}, fmt.Errorf("plugin jar must be between 1 byte and %d bytes", maxPluginJARSize)
	}
	if hasLegacyConfig && hasContent {
		return UploadResult{}, errors.New("config zip and content zip cannot be combined")
	}
	if hasContent {
		input.ContentType = strings.ToLower(strings.TrimSpace(input.ContentType))
		if !validContentType(input.ContentType) {
			return UploadResult{}, errors.New("content type must be config or full")
		}
	}

	var descriptors map[string]Descriptor
	var primary Descriptor
	var err error
	if hasJAR {
		descriptors, primary, err = ParseModJAR(input.JAR, input.JARFilename, input.PluginType)
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
	} else {
		// 仅内容包上传：身份来自表单 name/version；full 类型缺省时扫描 zip 内嵌 jar。
		name, version := strings.TrimSpace(input.ContentName), strings.TrimSpace(input.ContentVersion)
		if name != "" && version != "" {
			primary = Descriptor{Name: name, Version: version}
		} else if input.ContentType == ContentTypeFull {
			var found bool
			descriptors, primary, found = findJARInZIP(input.ContentZIP, input.PluginType)
			if !found {
				return UploadResult{}, errors.New("content bundle requires a name and version, or a recognizable jar inside")
			}
		} else {
			return UploadResult{}, errors.New("content bundle requires a name and version")
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	typeRoot := r.typeRoot(input.PluginType)
	pluginID, err := r.resolvePluginIDLocked(typeRoot, primary.Name)
	if err != nil {
		return UploadResult{}, err
	}
	pluginDir := filepath.Join(typeRoot, pluginID)
	index, _ := r.loadIndexLocked(pluginDir)
	if index.PluginID == "" {
		index = Index{
			SchemaVersion: manifestSchemaVersion, PluginID: pluginID, PluginType: input.PluginType,
			Name: primary.Name, AutoInstall: input.AutoInstall, NextArtifactID: 1,
		}
	}
	if !strings.EqualFold(index.Name, primary.Name) {
		return UploadResult{}, errors.New("plugin id is already used by another plugin name")
	}
	index.PluginType = input.PluginType
	index.AutoInstall = input.AutoInstall
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
	jarHashText := ""
	if hasJAR {
		jarPath := filepath.Join(staging, "plugin.jar")
		if err := os.WriteFile(jarPath, input.JAR, 0o640); err != nil {
			return UploadResult{}, err
		}
		jarHash := sha256.Sum256(input.JAR)
		jarHashText = hex.EncodeToString(jarHash[:])
	}
	config := ConfigSnapshot{Directory: configDirectory}
	if !hasContent {
		configPath := filepath.Join(staging, "config")
		if hasLegacyConfig {
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
	}
	var content *ContentSnapshot
	if hasContent {
		contentID := int64(1)
		contentRoot := contentVersionDir(staging, contentID)
		if err := os.MkdirAll(contentRoot, 0o750); err != nil {
			return UploadResult{}, err
		}
		if _, _, err := extractContentZIP(input.ContentZIP, contentRoot); err != nil {
			return UploadResult{}, err
		}
		snapshot := ContentSnapshot{ContentID: contentID, Type: input.ContentType, Present: true}
		snapshot.SHA256, snapshot.Files, snapshot.Size, err = treeHash(contentRoot)
		if err != nil {
			return UploadResult{}, err
		}
		if snapshot.Files == 0 {
			return UploadResult{}, errors.New("content bundle contains no files")
		}
		snapshot.Tree, err = contentTopLevelTree(contentRoot)
		if err != nil {
			return UploadResult{}, err
		}
		content = &snapshot
		if err := saveContentIndex(staging, ContentIndex{
			SchemaVersion: contentIndexSchemaVersion, Current: contentID, Versions: []ContentSnapshot{snapshot},
		}); err != nil {
			return UploadResult{}, err
		}
	}

	artifacts, err := r.loadArtifactsLocked(pluginDir)
	if err != nil {
		return UploadResult{}, err
	}
	for _, artifact := range artifacts {
		if artifact.Artifact.SHA256 == jarHashText && artifact.Config.SHA256 == config.SHA256 &&
			artifact.Config.Directory == config.Directory && contentMatches(artifact.Content, content) {
			if err := atomicYAML(filepath.Join(pluginDir, "index.yaml"), index); err != nil {
				return UploadResult{}, err
			}
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
		PluginID: pluginID, PluginType: input.PluginType,
		Name: primary.Name, Version: primary.Version,
		Main: primary.Main, Authors: append([]string(nil), primary.Authors...),
		Description: primary.Description, Website: primary.Website,
		ModMetadata: primary.ModMetadata, Descriptors: descriptors,
		Config: config, Content: content, UploadedBy: input.Uploader, UploadedAt: time.Now().UTC(),
	}
	if hasJAR {
		manifest.Artifact = ArtifactFile{
			File: "plugin.jar", OriginalFilename: input.JARFilename,
			SHA256: jarHashText, Size: int64(len(input.JAR)),
		}
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

// contentMatches 判断两个内容包快照是否一致（同类型同内容）；两者都为空视为一致。
func contentMatches(stored, current *ContentSnapshot) bool {
	if stored == nil || current == nil {
		return stored == nil && current == nil
	}
	return stored.Type == current.Type && stored.SHA256 == current.SHA256
}

func (r *Repository) List() ([]Plugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Plugin, 0)
	for _, pluginType := range repositoryTypes {
		entries, err := os.ReadDir(r.typeRoot(pluginType))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "import" || !pluginIDPattern.MatchString(entry.Name()) {
				continue
			}
			plugin, err := r.loadPluginLocked(pluginType, entry.Name())
			if err == nil {
				result = append(result, plugin)
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left].Name) < strings.ToLower(result[right].Name)
	})
	return result, nil
}

func (r *Repository) Get(pluginID string, pluginTypes ...string) (Plugin, error) {
	if !pluginIDPattern.MatchString(pluginID) {
		return Plugin{}, errors.New("invalid plugin id")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loadPluginLocked(normalizePluginType(pluginTypes), pluginID)
}

func (r *Repository) Artifact(pluginID string, artifactID int64, pluginTypes ...string) (Manifest, string, error) {
	if !pluginIDPattern.MatchString(pluginID) || artifactID < 1 {
		return Manifest{}, "", errors.New("invalid plugin artifact")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	pluginDir := filepath.Join(r.typeRoot(normalizePluginType(pluginTypes)), pluginID)
	manifest, err := r.loadManifestLocked(pluginDir, artifactID)
	if err != nil {
		return Manifest{}, "", err
	}
	return manifest, filepath.Join(pluginDir, strconv.FormatInt(artifactID, 10)), nil
}

func (r *Repository) loadPluginLocked(pluginType, pluginID string) (Plugin, error) {
	pluginDir := filepath.Join(r.typeRoot(pluginType), pluginID)
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

func (r *Repository) resolvePluginIDLocked(typeRoot, name string) (string, error) {
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
		pluginDir := filepath.Join(typeRoot, candidate)
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
	return Plugin{
		PluginID: index.PluginID, PluginType: index.PluginType, Name: index.Name,
		AutoInstall:       index.AutoInstall,
		CurrentArtifactID: index.CurrentArtifactID, Artifacts: artifacts}
}
