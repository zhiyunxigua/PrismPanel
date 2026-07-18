package api

import (
	"encoding/json"
	"net/http"
	"os"
)

type autoInstallResult struct {
	PluginType string          `json:"plugin_type"`
	PluginID   string          `json:"plugin_id"`
	Name       string          `json:"name"`
	Version    string          `json:"version"`
	Success    bool            `json:"success"`
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
		}
		path, manifest, buildErr := s.plugins.BuildBundle(
			plugin.PluginID, plugin.CurrentArtifactID, plugin.PluginType,
		)
		if path != "" {
			defer os.Remove(path)
		}
		item.Version = manifest.Version
		if buildErr == nil {
			buildErr = s.deployPluginBundle(request, nodeID, serverID, path, &item.Data)
		}
		item.Success = buildErr == nil
		if buildErr != nil {
			item.Error = buildErr.Error()
		}
		result = append(result, item)
	}
	return result
}

func hasAutoInstallFailure(results []autoInstallResult) bool {
	for _, result := range results {
		if !result.Success {
			return true
		}
	}
	return false
}
