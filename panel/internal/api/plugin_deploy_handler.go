package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func (s *Server) handlePluginArtifact(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/plugins/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 3 || parts[0] == "" || parts[2] != "deploy" || request.Method != http.MethodPost {
		http.NotFound(writer, request)
		return
	}
	if err := s.authorize(request, "plugin.deploy"); err != nil {
		writeRequestError(writer, err)
		return
	}
	artifactID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || artifactID < 1 {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact id"))
		return
	}
	var input struct {
		ServerID string `json:"server_id"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	if err == nil && (nodeID == "" || strings.TrimSpace(input.ServerID) == "") {
		err = apiError("INVALID_REQUEST", "node_id and server_id are required")
	}
	var result json.RawMessage
	if err == nil {
		var bundlePath string
		bundlePath, _, err = s.plugins.BuildBundle(parts[0], artifactID)
		if bundlePath != "" {
			defer os.Remove(bundlePath)
		}
		if err == nil {
			err = s.deployPluginBundle(request, nodeID, input.ServerID, bundlePath, &result)
		}
	}
	s.record(request, "plugin.deploy", parts[0], map[string]any{
		"artifact_id": artifactID, "node_id": nodeID, "server_id": input.ServerID,
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, result)
}
