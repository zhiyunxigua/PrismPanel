package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleInstanceAdmins(writer http.ResponseWriter, request *http.Request, instanceID string) {
	if !s.authorizeServerRequest(writer, request, "server.configure") {
		return
	}
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	if nodeID == "" || instanceID == "" {
		writeRequestError(writer, apiError("INVALID_REQUEST", "必须指定目标节点和实例"))
		return
	}
	if err := s.ensureInstanceExists(request, nodeID, instanceID); err != nil {
		writeRequestError(writer, err)
		return
	}
	switch request.Method {
	case http.MethodGet:
		admins, err := s.store.ListInstanceAdmins(request.Context(), nodeID, instanceID)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"admins": admins})
	case http.MethodPut:
		var input struct {
			UserIDs []string `json:"user_ids"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		if err == nil && len(input.UserIDs) > 100 {
			err = apiError("INVALID_REQUEST", "单个实例最多分配 100 名管理员")
		}
		var admins any
		if err == nil {
			admins, err = s.store.SetInstanceAdmins(request.Context(), nodeID, instanceID, input.UserIDs)
		}
		err = publicError(err)
		s.record(request, "instance.admins.update", instanceID, map[string]any{
			"node_id": nodeID, "user_count": len(input.UserIDs),
		}, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"admins": admins})
	default:
		methodNotAllowed(writer, "GET, PUT")
	}
}

func (s *Server) ensureInstanceExists(request *http.Request, nodeID, instanceID string) error {
	var result struct {
		Instances []struct {
			InstanceID string `json:"instance_id"`
		} `json:"instances"`
	}
	if err := s.connections.Call(request.Context(), nodeID, "server.list", map[string]any{}, &result); err != nil {
		return err
	}
	for _, item := range result.Instances {
		if item.InstanceID == instanceID {
			return nil
		}
	}
	return apiError("NOT_FOUND", "目标实例不存在")
}
