package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"PrismPanel/internal/store"
)

func (s *Server) authorizeInstanceRequest(writer http.ResponseWriter, request *http.Request, permission, instanceID string) bool {
	if err := s.authorizeInstance(request, permission, instanceID); err != nil {
		writeRequestError(writer, err)
		return false
	}
	return true
}

func (s *Server) authorizeInstance(request *http.Request, permission, instanceID string) error {
	if err := s.authorize(request, permission); err == nil {
		return nil
	}
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	if nodeID == "" || instanceID == "" {
		return apiError("FORBIDDEN", "无权执行此操作")
	}
	allowed, err := s.store.CanInstance(request.Context(), currentSession(request).User, permission, nodeID, instanceID)
	if err != nil {
		return err
	}
	if !allowed {
		return apiError("FORBIDDEN", "无权执行此操作")
	}
	return nil
}

func (s *Server) authorizeFileScope(request *http.Request, permission, nodeID, resourceType, resourceID string) error {
	if err := s.authorize(request, permission); err == nil {
		return nil
	}
	if resourceType != "instance" || resourceID == "" {
		return apiError("FORBIDDEN", "无权执行此操作")
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return apiError("FORBIDDEN", "无权执行此操作")
	}
	allowed, err := s.store.CanInstance(request.Context(), currentSession(request).User, permission, nodeID, resourceID)
	if err != nil {
		return err
	}
	if !allowed {
		return apiError("FORBIDDEN", "无权执行此操作")
	}
	return nil
}

func filterServerListResult(result json.RawMessage, nodeID string, adminSet map[string]struct{}, restrict bool,
	canViewPlayers, canViewPlugins bool) (json.RawMessage, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(result, &payload); err != nil {
		return result, false
	}
	var instances []json.RawMessage
	if err := json.Unmarshal(payload["instances"], &instances); err != nil {
		return result, false
	}
	visibleInstances := make([]json.RawMessage, 0, len(instances))
	visibleServers := make(map[string]struct{})
	for _, raw := range instances {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		instanceID, _ := item["instance_id"].(string)
		_, assigned := adminSet[nodeID+"\x00"+instanceID]
		if restrict && !assigned {
			continue
		}
		item["instance_admin"] = assigned
		if !canViewPlayers && !assigned {
			delete(item, "players")
		}
		if !canViewPlugins && !assigned {
			delete(item, "plugins")
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		visibleInstances = append(visibleInstances, encoded)
		if serverID, ok := item["server_id"].(string); ok {
			visibleServers[serverID] = struct{}{}
		}
	}
	var servers []json.RawMessage
	if err := json.Unmarshal(payload["servers"], &servers); err == nil {
		if restrict {
			filtered := make([]json.RawMessage, 0, len(servers))
			for _, raw := range servers {
				var item map[string]any
				if json.Unmarshal(raw, &item) != nil {
					continue
				}
				serverID, _ := item["server_id"].(string)
				if _, visible := visibleServers[serverID]; visible {
					filtered = append(filtered, raw)
				}
			}
			servers = filtered
		}
		encoded, err := json.Marshal(servers)
		if err == nil {
			payload["servers"] = encoded
		}
	}
	encoded, err := json.Marshal(visibleInstances)
	if err == nil {
		payload["instances"] = encoded
	}
	encoded, err = json.Marshal(payload)
	if err != nil {
		return result, len(visibleInstances) > 0
	}
	return encoded, len(visibleInstances) > 0
}

func (s *Server) instanceAdminSet(request *http.Request) (map[string]struct{}, error) {
	return s.store.InstanceAdminSet(request.Context(), currentSession(request).User.ID)
}

func instanceScopedPermissions() []string {
	return store.InstanceAdminPermissions()
}
