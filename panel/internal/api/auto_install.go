package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"
)

type autoInstallResult struct {
	PluginType string          `json:"plugin_type"`
	PluginID   string          `json:"plugin_id"`
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	Success    bool            `json:"success"`
	Attempts   int             `json:"attempts"`
	Data       json.RawMessage `json:"data,omitempty"`
	Error      string          `json:"error,omitempty"`
}

func (s *Server) hasAutoInstallPlugins(platform string) (bool, error) {
	pluginType := pluginTypeForPlatform(platform)
	catalog, err := s.plugins.List()
	if err != nil {
		return false, err
	}
	for _, plugin := range catalog {
		if plugin.PluginType == pluginType && plugin.AutoInstall && plugin.CurrentArtifactID > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) autoInstallPlugins(
	request *http.Request,
	nodeID, serverID, platform string,
) []autoInstallResult {
	pluginType := pluginTypeForPlatform(platform)
	catalog, err := s.plugins.List()
	if err != nil {
		return []autoInstallResult{{PluginType: pluginType, Success: false, Error: err.Error()}}
	}
	result := make([]autoInstallResult, 0)
	for _, plugin := range catalog {
		if plugin.PluginType != pluginType || !plugin.AutoInstall || plugin.CurrentArtifactID < 1 {
			continue
		}
		item := autoInstallResult{
			PluginType: plugin.PluginType,
			PluginID:   plugin.PluginID,
			Name:       plugin.Name,
			Attempts:   1,
		}
		path, manifest, buildErr := s.plugins.BuildBundle(
			plugin.PluginID, plugin.CurrentArtifactID, plugin.PluginType,
		)
		item.Version = manifest.Version
		if buildErr == nil {
			for attempt := 1; attempt <= 3; attempt++ {
				item.Attempts = attempt
				buildErr = s.deployPluginBundle(request, nodeID, serverID, path, &item.Data)
				if buildErr == nil || attempt == 3 || !waitAutoInstallRetry(request.Context(), attempt) {
					break
				}
			}
		}
		if path != "" {
			_ = os.Remove(path)
		}
		item.Success = buildErr == nil
		if buildErr != nil {
			item.Error = buildErr.Error()
		}
		result = append(result, item)
	}
	return result
}

func waitAutoInstallRetry(ctx context.Context, attempt int) bool {
	timer := time.NewTimer(time.Duration(attempt) * 250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func hasAutoInstallFailure(results []autoInstallResult) bool {
	for _, result := range results {
		if !result.Success {
			return true
		}
	}
	return false
}

func (s *Server) blockServerAutoStart(
	request *http.Request,
	serverID string,
	server map[string]any,
) error {
	configuredID, _ := server["server_id"].(string)
	if configuredID != serverID {
		return apiError("INVALID_RESPONSE", "服务器配置 ID 与请求不一致")
	}
	process, ok := server["process"].(map[string]any)
	if !ok {
		return apiError("INVALID_RESPONSE", "服务器配置缺少 process 字段")
	}
	autoStart, _ := process["auto_start"].(bool)
	if !autoStart {
		return nil
	}
	process["auto_start"] = false
	var ignored json.RawMessage
	return s.callNode(request, "server.update", server, &ignored)
}

func (s *Server) handleServerAutoInstall(writer http.ResponseWriter, request *http.Request, serverID string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if !s.authorizeServerRequest(writer, request, "plugin.deploy") {
		return
	}
	server := make(map[string]any)
	err := s.callNode(request, "server.get", map[string]any{"server_id": serverID}, &server)
	nodeID := request.URL.Query().Get("node_id")
	platform, _ := server["platform"].(string)
	results := make([]autoInstallResult, 0)
	if err == nil {
		results = s.autoInstallPlugins(request, nodeID, serverID, platform)
	}
	autoStartBlocked := false
	var blockError string
	if err == nil && hasAutoInstallFailure(results) {
		if blockErr := s.blockServerAutoStart(request, serverID, server); blockErr != nil {
			blockError = blockErr.Error()
		} else {
			autoStartBlocked = true
		}
	}
	s.record(request, "plugin.auto_install", serverID, map[string]any{
		"node_id": nodeID, "platform": platform, "auto_start_blocked": autoStartBlocked,
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{
		"auto_install": results, "auto_start_blocked": autoStartBlocked,
		"auto_start_block_error": blockError,
	})
}
