package plugins

import "time"

type Descriptor struct {
	PluginType   string       `json:"plugin_type"`
	ID           string       `json:"id,omitempty"`
	File         string       `json:"file"`
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Main         string       `json:"main,omitempty"`
	Bootstrapper string       `json:"bootstrapper,omitempty"`
	Loader       string       `json:"loader,omitempty"`
	APIVersion   string       `json:"api_version,omitempty"`
	Authors      []string     `json:"authors,omitempty"`
	Description  string       `json:"description,omitempty"`
	Website      string       `json:"website,omitempty"`
	Dependencies []string     `json:"dependencies,omitempty"`
	ModMetadata  *ModMetadata `json:"mod_metadata,omitempty"`
}

// ModDependency / ModEntrypoint / ModMetadata 与
// panel/internal/plugins/types.go 为双份对应实现（fabric.mod.json 深度解析），
// 保持一致，修改需同步。依赖解析以 panel 为准（仓库展示/关联），
// daemon 仅用于运行时自检与上报，启停仍以 .jar.disabled 重命名为准。
type ModDependency struct {
	ID           string `json:"id"`
	VersionRange string `json:"version_range,omitempty"`
}

type ModEntrypoint struct {
	Kind   string   `json:"kind"`
	Values []string `json:"values,omitempty"`
}

type ModMetadata struct {
	ID            string          `json:"id,omitempty"`
	SchemaVersion int             `json:"schema_version,omitempty"`
	Environment   string          `json:"environment,omitempty"`
	License       string          `json:"license,omitempty"`
	Icon          string          `json:"icon,omitempty"`
	Depends       []ModDependency `json:"depends,omitempty"`
	Suggests      []ModDependency `json:"suggests,omitempty"`
	Entrypoints   []ModEntrypoint `json:"entrypoints,omitempty"`
}

type FilePlugin struct {
	PluginType   string                `json:"plugin_type"`
	ID           string                `json:"id,omitempty"`
	Name         string                `json:"name"`
	Version      string                `json:"version"`
	Main         string                `json:"main,omitempty"`
	Authors      []string              `json:"authors,omitempty"`
	Description  string                `json:"description,omitempty"`
	Website      string                `json:"website,omitempty"`
	Dependencies []string              `json:"dependencies,omitempty"`
	Descriptors  map[string]Descriptor `json:"descriptors"`
	SourceFile   string                `json:"source_file"`
	SHA256       string                `json:"sha256"`
	Size         int64                 `json:"size"`
	ModifiedAt   time.Time             `json:"modified_at"`
	Enabled      bool                  `json:"enabled"`
}

type Plugin struct {
	FilePlugin
	FilePresent    bool     `json:"file_present"`
	Loaded         bool     `json:"loaded"`
	RuntimeVersion string   `json:"runtime_version,omitempty"`
	RuntimeMain    string   `json:"runtime_main,omitempty"`
	Status         string   `json:"status"`
	Issues         []string `json:"issues"`
	PendingRestart bool     `json:"pending_restart"`
}

type ListResult struct {
	InstanceID      string   `json:"instance_id"`
	PluginConnected bool     `json:"plugin_connected"`
	PendingRestart  bool     `json:"pending_restart"`
	Items           []Plugin `json:"items"`
	Warnings        []string `json:"warnings"`
	Directory       string   `json:"directory"`
	Kind            string   `json:"kind"`
}

type OperationInput struct {
	ServerID        string `json:"server_id"`
	PluginName      string `json:"plugin_name,omitempty"`
	DeleteConfig    bool   `json:"delete_config,omitempty"`
	ConfigDirectory string `json:"config_directory,omitempty"`
}

type TargetResult struct {
	Target         string `json:"target"`
	Status         string `json:"status"`
	PendingRestart bool   `json:"pending_restart"`
	Message        string `json:"message,omitempty"`
	// 内容包部署统计（仅 plugin.content.deploy 使用）。
	Applied     int    `json:"applied,omitempty"`
	Overwritten int    `json:"overwritten,omitempty"`
	Added       int    `json:"added,omitempty"`
	BackupPath  string `json:"backup_path,omitempty"`
}

type OperationResult struct {
	ServerID       string         `json:"server_id"`
	PluginName     string         `json:"plugin_name"`
	Version        string         `json:"version,omitempty"`
	PendingRestart bool           `json:"pending_restart"`
	Directory      string         `json:"directory,omitempty"`
	Targets        []TargetResult `json:"targets"`
}

type InstanceUploadResult struct {
	Outcome         string `json:"outcome"`
	InstanceID      string `json:"instance_id"`
	PluginName      string `json:"plugin_name"`
	PluginType      string `json:"plugin_type"`
	Version         string `json:"version"`
	SourceFile      string `json:"source_file,omitempty"`
	ExistingVersion string `json:"existing_version,omitempty"`
	ExistingFile    string `json:"existing_file,omitempty"`
	Replaced        bool   `json:"replaced"`
	PendingRestart  bool   `json:"pending_restart"`
	Directory       string `json:"directory,omitempty"`
}

// PendingItem 是 pending.list 返回的单个队列项视图。
// Status 为 "pending"（待重试）或 "failed"（永久失败/重试达阈值，移入侧写）。
type PendingItem struct {
	Type             string    `json:"type"`
	PluginType       string    `json:"plugin_type,omitempty"`
	PluginName       string    `json:"plugin_name,omitempty"`
	OriginalFilename string    `json:"original_filename,omitempty"`
	ConfigDirectory  string    `json:"config_directory,omitempty"`
	DeleteConfig     bool      `json:"delete_config,omitempty"`
	Directory        string    `json:"directory,omitempty"`
	BundleFile       string    `json:"bundle_file,omitempty"`
	BackupSnapshot   bool      `json:"backup_snapshot,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	Status           string    `json:"status"`
	Attempts         int       `json:"attempts,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	FailedAt         time.Time `json:"failed_at,omitempty"`
}
