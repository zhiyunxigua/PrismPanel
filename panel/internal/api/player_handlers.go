package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handlePlayerTransfer(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if err := s.authorize(request, "player.transfer"); err != nil {
		writeRequestError(writer, err)
		return
	}
	var input struct {
		NodeID         string `json:"node_id"`
		InstanceID     string `json:"instance_id"`
		PlayerUUID     string `json:"player_uuid"`
		TargetServerID string `json:"target_server_id"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	input.NodeID = strings.TrimSpace(input.NodeID)
	input.InstanceID = strings.TrimSpace(input.InstanceID)
	input.PlayerUUID = strings.TrimSpace(input.PlayerUUID)
	input.TargetServerID = strings.TrimSpace(input.TargetServerID)
	if err == nil && (input.NodeID == "" || input.InstanceID == "" ||
		input.PlayerUUID == "" || input.TargetServerID == "") {
		err = apiError("INVALID_REQUEST", "node_id, instance_id, player_uuid and target_server_id are required")
	}
	var result json.RawMessage
	if err == nil {
		err = s.connections.Call(request.Context(), input.NodeID, "player.transfer", map[string]string{
			"instance_id":      input.InstanceID,
			"player_uuid":      input.PlayerUUID,
			"target_server_id": input.TargetServerID,
		}, &result)
	}
	s.record(request, "player.transfer", input.PlayerUUID, input, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, result)
}
