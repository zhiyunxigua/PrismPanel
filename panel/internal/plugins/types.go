package plugins

import "time"

type Uploader struct {
	UserID      string `yaml:"user_id" json:"user_id"`
	Username    string `yaml:"username" json:"username"`
	DisplayName string `yaml:"display_name" json:"display_name"`
}

type Descriptor struct {
	PluginType   string       `yaml:"plugin_type" json:"plugin_type"`
	ID           string       `yaml:"id,omitempty" json:"id,omitempty"`
	File         string       `yaml:"file" json:"file"`
	Name         string       `yaml:"name" json:"name"`
	Version      string       `yaml:"version" json:"version"`
	Main         string       `yaml:"main,omitempty" json:"main,omitempty"`
	Bootstrapper string       `yaml:"bootstrapper,omitempty" json:"bootstrapper,omitempty"`
	Loader       string       `yaml:"loader,omitempty" json:"loader,omitempty"`
	APIVersion   string       `yaml:"api_version,omitempty" json:"api_version,omitempty"`
	Authors      []string     `yaml:"authors,omitempty" json:"authors,omitempty"`
	Description  string       `yaml:"description,omitempty" json:"description,omitempty"`
	Website      string       `yaml:"website,omitempty" json:"website,omitempty"`
	Dependencies []string     `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	ModMetadata  *ModMetadata `yaml:"mod_metadata,omitempty" json:"mod_metadata,omitempty"`
}

// ModDependency 是 mod 依赖项（fabric.mod.json depends/suggests 的单个条目）：
// modid + 版本约束（*、>=1.20、[1.19,1.21) 等；多约束用 " || " 连接）。
type ModDependency struct {
	ID           string `yaml:"id" json:"id"`
	VersionRange string `yaml:"version_range,omitempty" json:"version_range,omitempty"`
}

// ModEntrypoint 是 mod 入口点（fabric.mod.json entrypoints 的单个 kind）。
type ModEntrypoint struct {
	Kind   string   `yaml:"kind" json:"kind"`
	Values []string `yaml:"values,omitempty" json:"values,omitempty"`
}

// ModMetadata 保存 Fabric mod 的深度元数据（fabric.mod.json 解析产物）。
// 依赖解析以 panel 为准（仓库展示与关联），daemon 端 descriptor_platform.go
// 有同结构实现，修改需同步。
type ModMetadata struct {
	ID            string          `yaml:"id,omitempty" json:"id,omitempty"`
	SchemaVersion int             `yaml:"schema_version,omitempty" json:"schema_version,omitempty"`
	Environment   string          `yaml:"environment,omitempty" json:"environment,omitempty"`
	License       string          `yaml:"license,omitempty" json:"license,omitempty"`
	Icon          string          `yaml:"icon,omitempty" json:"icon,omitempty"`
	Depends       []ModDependency `yaml:"depends,omitempty" json:"depends,omitempty"`
	Suggests      []ModDependency `yaml:"suggests,omitempty" json:"suggests,omitempty"`
	Entrypoints   []ModEntrypoint `yaml:"entrypoints,omitempty" json:"entrypoints,omitempty"`
}

type ArtifactFile struct {
	File             string `yaml:"file" json:"file"`
	OriginalFilename string `yaml:"original_filename" json:"original_filename"`
	SHA256           string `yaml:"sha256" json:"sha256"`
	Size             int64  `yaml:"size" json:"size"`
}

type ConfigSnapshot struct {
	Directory string `yaml:"directory" json:"directory"`
	Present   bool   `yaml:"present" json:"present"`
	Inherited bool   `yaml:"inherited" json:"inherited"`
	SHA256    string `yaml:"sha256,omitempty" json:"sha256,omitempty"`
	Files     int    `yaml:"files,omitempty" json:"files,omitempty"`
	Size      int64  `yaml:"size,omitempty" json:"size,omitempty"`
}

type Manifest struct {
	SchemaVersion int                   `yaml:"schema_version" json:"schema_version"`
	ArtifactID    int64                 `yaml:"artifact_id" json:"artifact_id"`
	PluginID      string                `yaml:"plugin_id" json:"plugin_id"`
	PluginType    string                `yaml:"plugin_type" json:"plugin_type"`
	Name          string                `yaml:"name" json:"name"`
	Version       string                `yaml:"version" json:"version"`
	Main          string                `yaml:"main,omitempty" json:"main,omitempty"`
	Authors       []string              `yaml:"authors,omitempty" json:"authors,omitempty"`
	Description   string                `yaml:"description,omitempty" json:"description,omitempty"`
	Website       string                `yaml:"website,omitempty" json:"website,omitempty"`
	ModMetadata   *ModMetadata          `yaml:"mod_metadata,omitempty" json:"mod_metadata,omitempty"`
	Descriptors   map[string]Descriptor `yaml:"descriptors" json:"descriptors"`
	Artifact      ArtifactFile          `yaml:"artifact" json:"artifact"`
	Config        ConfigSnapshot        `yaml:"config" json:"config"`
	UploadedBy    Uploader              `yaml:"uploaded_by" json:"uploaded_by"`
	UploadedAt    time.Time             `yaml:"uploaded_at" json:"uploaded_at"`
}

type Index struct {
	SchemaVersion     int    `yaml:"schema_version" json:"schema_version"`
	PluginID          string `yaml:"plugin_id" json:"plugin_id"`
	PluginType        string `yaml:"plugin_type" json:"plugin_type"`
	Name              string `yaml:"name" json:"name"`
	AutoInstall       bool   `yaml:"auto_install" json:"auto_install"`
	CurrentArtifactID int64  `yaml:"current_artifact_id" json:"current_artifact_id"`
	NextArtifactID    int64  `yaml:"next_artifact_id" json:"next_artifact_id"`
}

type Plugin struct {
	PluginID          string     `json:"plugin_id"`
	PluginType        string     `json:"plugin_type"`
	Name              string     `json:"name"`
	AutoInstall       bool       `json:"auto_install"`
	CurrentArtifactID int64      `json:"current_artifact_id"`
	Artifacts         []Manifest `json:"artifacts"`
}

type UploadInput struct {
	PluginType      string
	AutoInstall     bool
	JARFilename     string
	JAR             []byte
	ConfigZIP       []byte
	ConfigDirectory string
	Uploader        Uploader
}

type UploadResult struct {
	Plugin    Plugin   `json:"plugin"`
	Artifact  Manifest `json:"artifact"`
	Duplicate bool     `json:"duplicate"`
}

type ScanReport struct {
	Plugins          []Plugin `json:"plugins"`
	Imported         int      `json:"imported"`
	Duplicates       int      `json:"duplicates"`
	RebuiltManifests int      `json:"rebuilt_manifests"`
	RecoveredChanges int      `json:"recovered_changes"`
	Warnings         []string `json:"warnings"`
}
