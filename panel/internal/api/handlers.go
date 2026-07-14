package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"PrismPanel/internal/audit"
	"PrismPanel/internal/daemon"
)

func (s *Server) handleServers(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		var result json.RawMessage
		if err := s.daemon.Call(request.Context(), "server.list", map[string]any{}, &result); err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
	case http.MethodPost:
		var result json.RawMessage
		body, err := readBody(request)
		if err == nil {
			err = s.daemon.Call(request.Context(), "server.create", body, &result)
		}
		s.record(request, "server.create", "", body, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
	default:
		writer.Header().Set("Allow", "GET, POST")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleServer(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/servers/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "deploy" {
		s.handleServerDeployment(writer, request, parts[0])
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	serverID := parts[0]
	switch request.Method {
	case http.MethodGet:
		var result json.RawMessage
		err := s.daemon.Call(request.Context(), "server.get", map[string]any{"server_id": serverID}, &result)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
	case http.MethodPut:
		var result json.RawMessage
		body, err := readBody(request)
		if err == nil {
			var identity struct {
				ServerID string `json:"server_id"`
			}
			if decodeErr := json.Unmarshal(body, &identity); decodeErr != nil || identity.ServerID != serverID {
				err = &daemon.APIError{Code: "SERVER_ID_IMMUTABLE", Message: "URL 与配置中的 server_id 必须一致"}
			}
		}
		if err == nil {
			err = s.daemon.Call(request.Context(), "server.update", body, &result)
		}
		s.record(request, "server.update", serverID, body, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
	case http.MethodDelete:
		err := s.daemon.Call(
			request.Context(), "server.delete", map[string]any{"server_id": serverID}, nil,
		)
		s.record(request, "server.delete", serverID, nil, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	default:
		writer.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleServerDeployment(writer http.ResponseWriter, request *http.Request, serverID string) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Targets []int `json:"targets,omitempty"`
	}
	body, err := readBody(request)
	if err == nil {
		if decodeErr := json.Unmarshal(body, &input); decodeErr != nil {
			err = &daemon.APIError{Code: "INVALID_REQUEST", Message: "部署目标格式无效"}
		}
	}
	var result json.RawMessage
	if err == nil {
		err = s.daemon.Call(request.Context(), "deployment.start", map[string]any{
			"server_id": serverID, "targets": input.Targets,
		}, &result)
	}
	s.record(request, "deployment.start", serverID, input, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (s *Server) handleDeployment(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/deployments/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	taskID := parts[0]
	if len(parts) == 1 && request.Method == http.MethodGet {
		var result json.RawMessage
		err := s.daemon.Call(request.Context(), "deployment.get", map[string]any{"task_id": taskID}, &result)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
		return
	}
	if len(parts) == 2 && request.Method == http.MethodPost && (parts[1] == "cancel" || parts[1] == "force-stop") {
		messageType := "deployment.cancel"
		if parts[1] == "force-stop" {
			messageType = "deployment.force_stop"
		}
		var result json.RawMessage
		err := s.daemon.Call(request.Context(), messageType, map[string]any{"task_id": taskID}, &result)
		s.record(request, messageType, taskID, nil, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
		return
	}
	http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) record(request *http.Request, action, target string, detail any, err error) {
	entry := audit.Entry{
		Actor: actor(request), Action: action, Target: target, Success: err == nil, Detail: detail,
	}
	var apiError *daemon.APIError
	if errors.As(err, &apiError) {
		entry.ErrorCode = apiError.Code
	}
	if auditErr := s.audit.Write(entry); auditErr != nil {
		s.logger.Error("write audit log", "error", auditErr)
	}
}

func (s *Server) handleInstance(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/instances/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	instanceID, action := parts[0], parts[1]
	switch action {
	case "start", "stop", "restart", "kill":
		messageType := "instance." + action
		var result json.RawMessage
		err := s.daemon.Call(
			request.Context(), messageType, map[string]any{"instance_id": instanceID}, &result,
		)
		s.record(request, messageType, instanceID, nil, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
	case "command":
		var input struct {
			Command string `json:"command"`
		}
		body, err := readBody(request)
		if err == nil {
			if decodeErr := json.Unmarshal(body, &input); decodeErr != nil {
				err = &daemon.APIError{Code: "INVALID_REQUEST", Message: "命令请求格式无效"}
			}
		}
		if err == nil {
			err = s.daemon.Call(request.Context(), "console.command", map[string]any{
				"instance_id": instanceID, "command": input.Command,
			}, nil)
		}
		s.record(request, "console.command", instanceID, map[string]any{"command": input.Command}, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	case "console-ticket":
		s.handleConsoleTicket(writer, request, instanceID)
	default:
		http.NotFound(writer, request)
	}
}

func (s *Server) handleConsoleTicket(writer http.ResponseWriter, request *http.Request, instanceID string) {
	var created struct {
		TicketID  string `json:"ticket_id"`
		Ticket    string `json:"ticket"`
		ExpiresAt string `json:"expires_at"`
		PublicURL string `json:"public_url"`
	}
	err := s.daemon.Call(request.Context(), "ticket.create", map[string]any{
		"scope": "console.read", "instance_id": instanceID, "ttl_seconds": 120,
	}, &created)
	if err != nil {
		s.record(request, "console.subscribe", instanceID, nil, err)
		writeError(writer, err)
		return
	}
	endpoint := "/api/v1/ws/console"
	if created.PublicURL != "" {
		endpoint, err = daemon.PublicConsoleURL(created.PublicURL)
	}
	s.record(request, "console.subscribe", instanceID, nil, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{
		"ticket_id": created.TicketID, "ticket": created.Ticket,
		"expires_at": created.ExpiresAt, "instance_id": instanceID,
		"websocket_url": endpoint, "direct": created.PublicURL != "",
	})
}
