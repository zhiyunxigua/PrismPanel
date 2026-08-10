package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/store"
)

func (s *Server) handlePluginArtifact(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/plugins/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[2] == "deploy" {
		s.handlePluginDeployment(writer, request, "spigot", parts[0], parts[1], false)
		return
	}
	if len(parts) < 4 {
		http.NotFound(writer, request)
		return
	}
	pluginType, pluginID, artifactPart := parts[0], parts[1], parts[2]
	if parts[3] == "deploy" && len(parts) == 4 {
		s.handlePluginDeployment(writer, request, pluginType, pluginID, artifactPart, false)
		return
	}
	if parts[3] == "config" && len(parts) == 4 {
		s.handlePluginConfig(writer, request, pluginType, pluginID, artifactPart)
		return
	}
	if parts[3] == "config" && len(parts) == 5 && parts[4] == "deploy" {
		s.handlePluginDeployment(writer, request, pluginType, pluginID, artifactPart, true)
		return
	}
	http.NotFound(writer, request)
}

func (s *Server) handlePluginDeployment(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string, configOnly bool) {
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
		if configOnly {
			bundlePath, _, err = s.plugins.BuildConfigBundle(pluginID, artifactID, pluginType)
		} else {
			bundlePath, _, err = s.plugins.BuildBundle(pluginID, artifactID, pluginType)
		}
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
					if configOnly {
						deployErr = s.deployPluginConfigBundle(request, item.NodeID, item.ServerID, bundlePath, &item.Data)
					} else {
						deployErr = s.deployPluginBundle(request, item.NodeID, item.ServerID, bundlePath, &item.Data)
					}
					if deployErr != nil {
						item.Error = deployErr.Error()
					}
				}
				results = append(results, item)
			}
		}
	}
	action := "plugin.deploy"
	if configOnly {
		action = "plugin.config.deploy"
	}
	s.record(request, action, pluginType+"/"+pluginID, map[string]any{
		"artifact_id": artifactID, "targets": input.Targets, "results": results,
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"targets": results})
}

func (s *Server) handlePluginConfig(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string) {
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 || !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact"))
		return
	}
	path := request.URL.Query().Get("path")
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, "plugin.view"); err != nil {
			writeRequestError(writer, err)
			return
		}
		if strings.TrimSpace(path) == "" {
			files, err := s.plugins.ListConfig(pluginID, artifactID, pluginType)
			if err != nil {
				writeError(writer, err)
				return
			}
			writeSuccess(writer, map[string]any{"files": files})
			return
		}
		contents, err := s.plugins.ReadConfig(pluginID, artifactID, path, pluginType)
		if err != nil {
			writeError(writer, err)
			return
		}
		if !utf8.Valid(contents) {
			writeRequestError(writer, apiError("UNSUPPORTED_ENCODING", "配置文件不是 UTF-8 文本"))
			return
		}
		writeSuccess(writer, map[string]any{"path": path, "content": string(contents)})
	case http.MethodPut:
		if err := s.authorize(request, "plugin.upload"); err != nil {
			writeRequestError(writer, err)
			return
		}
		var input struct {
			Content string `json:"content"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		if err == nil {
			if strings.TrimSpace(path) == "" {
				err = apiError("INVALID_REQUEST", "config file path is required")
			} else {
				_, err = s.plugins.UpdateConfig(pluginID, artifactID, path, []byte(input.Content), pluginType)
			}
		}
		if err == nil {
			var catalog []panelplugins.Plugin
			catalog, err = s.plugins.List()
			if err == nil {
				err = syncPluginCatalogAll(request.Context(), s.store, catalog)
			}
		}
		s.record(request, "plugin.config.update", pluginType+"/"+pluginID, map[string]any{
			"artifact_id": artifactID, "path": path,
		}, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"path": path})
	default:
		methodNotAllowed(writer, "GET, PUT")
	}
}
