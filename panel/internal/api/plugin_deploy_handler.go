package api

import (
	"PrismPanel/internal/store"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func (s *Server) handlePluginArtifact(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/plugins/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 {
		s.deletePlugin(writer, request, parts[0], parts[1])
		return
	}
	if len(parts) == 3 && parts[2] == "deploy" {
		s.handlePluginDeployment(writer, request, "spigot", parts[0], parts[1])
		return
	}
	if len(parts) < 4 {
		http.NotFound(writer, request)
		return
	}
	pluginType, pluginID, artifactPart := parts[0], parts[1], parts[2]
	if parts[3] == "deploy" && len(parts) == 4 {
		s.handlePluginDeployment(writer, request, pluginType, pluginID, artifactPart)
		return
	}
	http.NotFound(writer, request)
}

func (s *Server) handlePluginDeployment(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if err := s.authorize(request, "plugin.deploy"); err != nil {
		writeRequestError(writer, err)
		return
	}
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact id"))
		return
	}
	var input struct {
		ServerID string             `json:"server_id"`
		Targets  []deploymentTarget `json:"targets"`
		Rules    []store.TargetRule `json:"rules"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	if err == nil && input.Rules != nil {
		err = s.store.ReplacePluginDeployPreferences(
			request.Context(), pluginType, pluginID, input.Rules,
		)
		if err == nil {
			var catalog []catalogNode
			catalog, err = s.loadCatalog(request.Context())
			if err == nil {
				input.Targets = resolveSelectedServers(catalog, input.Rules, pluginType)
			}
		}
	}
	if len(input.Targets) == 0 && nodeID != "" && strings.TrimSpace(input.ServerID) != "" {
		input.Targets = append(input.Targets, deploymentTarget{NodeID: nodeID, ServerID: input.ServerID})
	}
	if err == nil && len(input.Targets) == 0 {
		err = apiError("INVALID_REQUEST", "node_id and server_id are required")
	}
	type deploymentResult struct {
		NodeID   string          `json:"node_id"`
		ServerID string          `json:"server_id"`
		Data     json.RawMessage `json:"data,omitempty"`
		Error    string          `json:"error,omitempty"`
	}
	results := make([]deploymentResult, 0, len(input.Targets))
	if err == nil {
		var bundlePath string
		bundlePath, _, err = s.plugins.BuildBundle(pluginID, artifactID, pluginType)
		if bundlePath != "" {
			defer os.Remove(bundlePath)
		}
		if err == nil {
			for _, target := range input.Targets {
				item := deploymentResult{
					NodeID:   strings.TrimSpace(target.NodeID),
					ServerID: strings.TrimSpace(target.ServerID),
				}
				if item.NodeID == "" || item.ServerID == "" {
					item.Error = "node_id and server_id are required"
				} else {
					item.Error = ""
					var deployErr error
					deployErr = s.deployPluginBundle(request, item.NodeID, item.ServerID, bundlePath, &item.Data)
					if deployErr != nil {
						item.Error = deployErr.Error()
					}
				}
				results = append(results, item)
			}
		}
	}
	action := "plugin.deploy"
	s.record(request, action, pluginType+"/"+pluginID, map[string]any{
		"artifact_id": artifactID, "targets": input.Targets, "results": results,
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"targets": results})
}
