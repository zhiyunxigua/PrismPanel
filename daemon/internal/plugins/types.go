package plugins

import "time"

type Descriptor struct {
	File         string   `json:"file"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Main         string   `json:"main,omitempty"`
	Bootstrapper string   `json:"bootstrapper,omitempty"`
	Loader       string   `json:"loader,omitempty"`
	APIVersion   string   `json:"api_version,omitempty"`
	Authors      []string `json:"authors,omitempty"`
	Description  string   `json:"description,omitempty"`
	Website      string   `json:"website,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type FilePlugin struct {
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
	RuntimeSHA256  string   `json:"runtime_sha256,omitempty"`
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
}

type OperationResult struct {
	ServerID       string         `json:"server_id"`
	PluginName     string         `json:"plugin_name"`
	Version        string         `json:"version,omitempty"`
	PendingRestart bool           `json:"pending_restart"`
	Targets        []TargetResult `json:"targets"`
}
