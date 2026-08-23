package plugins

import "time"

type Descriptor struct {
	PluginType   string   `json:"plugin_type"`
	ID           string   `json:"id,omitempty"`
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
