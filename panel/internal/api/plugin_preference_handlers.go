package api

import (
	"encoding/json"
	"net/http"
	"strings"

	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/store"
)

func (s *Server) handlePluginDeployPreferences(writer http.ResponseWriter, request *http.Request) {
	if err := s.authorize(request, "plugin.deploy"); err != nil {
		writeRequestError(writer, err)
		return
	}
	pluginType := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("plugin_type")))
	pluginID := strings.TrimSpace(request.URL.Query().Get("plugin_id"))
	if !panelplugins.ValidPluginType(pluginType) || pluginID == "" {
		writeRequestError(writer, apiError("INVALID_REQUEST", "valid plugin_type and plugin_id are required"))
		return
	}
	switch request.Method {
	case http.MethodGet:
		rules, err := s.store.PluginDeployPreferences(request.Context(), pluginType, pluginID)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"rules": rules})
	case http.MethodPut:
		var input struct {
			Rules []store.TargetRule `json:"rules"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		if err == nil {
			err = s.store.ReplacePluginDeployPreferences(
				request.Context(), pluginType, pluginID, input.Rules,
			)
		}
		s.record(request, "plugin.deploy.preferences", pluginType+"/"+pluginID, input, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, input)
	default:
		methodNotAllowed(writer, "GET, PUT")
	}
}
