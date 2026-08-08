package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"PrismPanel/internal/consolecommand"
	"PrismPanel/internal/daemon"
	"PrismPanel/internal/store"
)

type serverNodeSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type serverNodeContent struct {
	Node      serverNodeSummary `json:"node"`
	Servers   []json.RawMessage `json:"servers"`
	Instances []json.RawMessage `json:"instances"`
	Error     *APIError         `json:"error,omitempty"`
}

func (s *Server) handleServers(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if !s.authorizeServerRequest(writer, request, "server.view") {
			return
		}
		if strings.TrimSpace(request.URL.Query().Get("node_id")) == "" {
			s.handleAllServers(writer, request)
			return
		}
		var result json.RawMessage
		if err := s.callNode(request, "server.list", map[string]any{}, &result); err != nil {
			writeError(writer, err)
			return
		}
		result = sanitizeServerListResult(
			result, s.allow(request, "player.view"), s.allow(request, "plugin.view"),
		)
		writeSuccess(writer, result)
	case http.MethodPost:
		if !s.authorizeServerRequest(writer, request, "server.create") {
			return
		}
		var result json.RawMessage
		var created struct {
			ServerID string `json:"server_id"`
			Platform string `json:"platform"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &created)
		}
		if err == nil {
			var required bool
			required, err = s.hasAutoInstallPlugins(created.Platform)
			if err == nil && required && !s.allow(request, "plugin.deploy") {
				err = apiError("FORBIDDEN", "创建该服务器需要插件部署权限，原因是存在自动安装插件")
			}
		}
		if err == nil {
			err = s.callNode(request, "server.create", body, &result)
		}
		s.record(request, "server.create", "", body, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
		deferAutoInstall, _ := strconv.ParseBool(request.URL.Query().Get("defer_auto_install"))
		autoInstall := make([]autoInstallResult, 0)
		if !deferAutoInstall {
			autoInstall = s.autoInstallPlugins(request, nodeID, created.ServerID, created.Platform)
		}
		autoStartBlocked := false
		var autoStartBlockError string
		if hasAutoInstallFailure(autoInstall) {
			var server map[string]any
			if decodeErr := json.Unmarshal(body, &server); decodeErr != nil {
				autoStartBlockError = decodeErr.Error()
			} else if blockErr := s.blockServerAutoStart(request, created.ServerID, server); blockErr != nil {
				autoStartBlockError = blockErr.Error()
			} else {
				autoStartBlocked = true
			}
		}
		go s.reconcileAllProxies(context.Background())
		writeSuccess(writer, map[string]any{
			"server":                 result,
			"auto_install":           autoInstall,
			"auto_start_blocked":     autoStartBlocked,
			"auto_start_block_error": autoStartBlockError,
			"auto_install_deferred":  deferAutoInstall,
		})
	default:
		writer.Header().Set("Allow", "GET, POST")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAllServers(writer http.ResponseWriter, request *http.Request) {
	canViewPlayers := s.allow(request, "player.view")
	canViewPlugins := s.allow(request, "plugin.view")
	nodes, err := s.nodes.List(request.Context())
	if err != nil {
		writeError(writer, err)
		return
	}
	items := make([]serverNodeContent, len(nodes))
	var wait sync.WaitGroup
	for index, node := range nodes {
		items[index].Node = serverNodeSummary{ID: node.ID, Name: node.Name, Status: node.Status}
		wait.Add(1)
		go func(targetIndex int, nodeID string) {
			defer wait.Done()
			var content struct {
				Servers   []json.RawMessage `json:"servers"`
				Instances []json.RawMessage `json:"instances"`
			}
			callContext, cancel := context.WithTimeout(request.Context(), 5*time.Second)
			defer cancel()
			callErr := s.connections.Call(callContext, nodeID, "server.list", map[string]any{}, &content)
			if callErr != nil {
				items[targetIndex].Error = serverListError(callErr)
				items[targetIndex].Servers = []json.RawMessage{}
				items[targetIndex].Instances = []json.RawMessage{}
				return
			}
			items[targetIndex].Servers = content.Servers
			items[targetIndex].Instances = sanitizeInstanceMessages(
				content.Instances, canViewPlayers, canViewPlugins,
			)
		}(index, node.ID)
	}
	wait.Wait()
	writeSuccess(writer, map[string]any{"nodes": items})
}

func sanitizeServerListResult(result json.RawMessage, canViewPlayers, canViewPlugins bool) json.RawMessage {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(result, &payload); err != nil {
		return result
	}
	var instances []json.RawMessage
	if err := json.Unmarshal(payload["instances"], &instances); err != nil {
		return result
	}
	encoded, err := json.Marshal(sanitizeInstanceMessages(instances, canViewPlayers, canViewPlugins))
	if err != nil {
		return result
	}
	payload["instances"] = encoded
	encoded, err = json.Marshal(payload)
	if err != nil {
		return result
	}
	return encoded
}

func sanitizeInstanceMessages(instances []json.RawMessage, canViewPlayers, canViewPlugins bool) []json.RawMessage {
	result := make([]json.RawMessage, len(instances))
	for index, raw := range instances {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			result[index] = raw
			continue
		}
		if !canViewPlayers {
			delete(item, "players")
		}
		if !canViewPlugins {
			delete(item, "plugins")
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			result[index] = raw
			continue
		}
		result[index] = encoded
	}
	return result
}

func serverListError(err error) *APIError {
	if errors.Is(err, daemon.ErrDisconnected) {
		return apiError("DAEMON_UNAVAILABLE", "守护进程当前不可用")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apiError("DAEMON_TIMEOUT", "读取守护进程服务器列表超时")
	}
	var daemonError *daemon.APIError
	if errors.As(err, &daemonError) {
		return apiError(daemonError.Code, daemonError.Message)
	}
	return apiError("INTERNAL", "无法读取节点服务器列表")
}

func (s *Server) authorizeServerRequest(writer http.ResponseWriter, request *http.Request, permission string) bool {
	if err := s.authorize(request, permission); err != nil {
		writeRequestError(writer, err)
		return false
	}
	return true
}

func (s *Server) handleServer(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/servers/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[1] == "plugins" && parts[2] == "auto-install" {
		s.handleServerAutoInstall(writer, request, parts[0])
		return
	}
	if len(parts) == 3 && parts[1] == "plugins" {
		s.handleServerPluginOperation(writer, request, parts[0], parts[2])
		return
	}
	if len(parts) == 2 && parts[1] == "deploy" {
		s.handleServerDeployment(writer, request, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "deployment" {
		s.handleActiveServerDeployment(writer, request, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "metrics" {
		s.handleServerMetrics(writer, request, parts[0])
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	serverID := parts[0]
	switch request.Method {
	case http.MethodGet:
		if !s.authorizeServerRequest(writer, request, "server.view") {
			return
		}
		var result json.RawMessage
		err := s.callNode(request, "server.get", map[string]any{"server_id": serverID}, &result)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
	case http.MethodPut:
		if !s.authorizeServerRequest(writer, request, "server.configure") {
			return
		}
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
			err = s.callNode(request, "server.update", body, &result)
		}
		s.record(request, "server.update", serverID, body, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		go s.reconcileAllProxies(context.Background())
		writeSuccess(writer, result)
	case http.MethodDelete:
		if !s.authorizeServerRequest(writer, request, "server.delete") {
			return
		}
		err := s.callNode(request, "server.delete", map[string]any{"server_id": serverID}, nil)
		s.record(request, "server.delete", serverID, nil, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
		if cleanupErr := s.store.DeleteProxySyncOwner(request.Context(), nodeID, serverID); cleanupErr != nil {
			s.logger.Error("delete proxy sync owner", "node_id", nodeID, "server_id", serverID, "error", cleanupErr)
		}
		if cleanupErr := s.store.DeleteProxySyncTarget(request.Context(), nodeID, serverID); cleanupErr != nil {
			s.logger.Error("delete proxy sync target", "node_id", nodeID, "server_id", serverID, "error", cleanupErr)
		}
		go s.reconcileAllProxies(context.Background())
		writeSuccess(writer, map[string]any{})
	default:
		writer.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleServerPluginOperation(writer http.ResponseWriter, request *http.Request, serverID, action string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	permission := "plugin.deploy"
	if action == "uninstall" {
		permission = "plugin.remove"
	}
	if action != "enable" && action != "disable" && action != "uninstall" {
		http.NotFound(writer, request)
		return
	}
	if !s.authorizeServerRequest(writer, request, permission) {
		return
	}
	var input struct {
		PluginName      string `json:"plugin_name"`
		DeleteConfig    bool   `json:"delete_config,omitempty"`
		ConfigDirectory string `json:"config_directory,omitempty"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	if err == nil && strings.TrimSpace(input.PluginName) == "" {
		err = apiError("INVALID_REQUEST", "�{���}�")
	}
	var result json.RawMessage
	messageType := "plugin." + action
	if err == nil {
		err = s.callNode(request, messageType, map[string]any{
			"server_id": serverID, "plugin_name": input.PluginName,
			"delete_config": input.DeleteConfig, "config_directory": input.ConfigDirectory,
		}, &result)
	}
	s.record(request, messageType, serverID, input, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (s *Server) handleServerDeployment(writer http.ResponseWriter, request *http.Request, serverID string) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeServerRequest(writer, request, "server.deploy") {
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
		err = s.callNode(request, "deployment.start", map[string]any{
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

func (s *Server) handleActiveServerDeployment(writer http.ResponseWriter, request *http.Request, serverID string) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", "GET")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allow(request, "task.view") && !s.allow(request, "server.deploy") {
		writeRequestError(writer, apiError("FORBIDDEN", "无权查看部署任务"))
		return
	}
	var result json.RawMessage
	err := s.callNode(request, "deployment.active", map[string]any{"server_id": serverID}, &result)
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
		if !s.allow(request, "task.view") && !s.allow(request, "server.deploy") {
			writeRequestError(writer, apiError("FORBIDDEN", "无权查看部署任务"))
			return
		}
		var result json.RawMessage
		err := s.callNode(request, "deployment.get", map[string]any{"task_id": taskID}, &result)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
		return
	}
	if len(parts) == 2 && request.Method == http.MethodPost && (parts[1] == "cancel" || parts[1] == "force-stop") {
		if !s.authorizeServerRequest(writer, request, "task.cancel") {
			return
		}
		messageType := "deployment.cancel"
		if parts[1] == "force-stop" {
			messageType = "deployment.force_stop"
		}
		var result json.RawMessage
		err := s.callNode(request, messageType, map[string]any{"task_id": taskID}, &result)
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
	entry := store.AuditLog{
		RequestID: requestID(request), Action: action, ResourceID: target,
		ResourceType: resourceType(action), RiskLevel: riskLevel(action),
		Success: err == nil, SourceIP: clientIP(request), UserAgent: request.UserAgent(),
		Detail: detailMap(detail),
	}
	session := currentSession(request)
	entry.ActorUserID = session.User.ID
	if len(session.TokenHash) > 0 {
		entry.SessionID = hex.EncodeToString(session.TokenHash)
	}
	entry.ActorUsername = session.User.Username
	entry.ActorDisplayName = session.User.DisplayName
	var apiError *daemon.APIError
	if errors.As(err, &apiError) {
		entry.ErrorCode = apiError.Code
	} else {
		entry.ErrorCode = errorCode(err)
	}
	s.writeAudit(entry)
}

func resourceType(action string) string {
	prefix, _, _ := strings.Cut(action, ".")
	switch prefix {
	case "auth":
		return "session"
	case "instance", "console":
		return "instance"
	case "operator":
		return "operator"
	case "permission":
		return "permission_grant"
	default:
		return prefix
	}
}

func riskLevel(action string) string {
	switch {
	case action == "winapp.release.publish":
		return "critical"
	case strings.HasPrefix(action, "firewall.system"):
		return "critical"
	case strings.HasPrefix(action, "firewall."):
		return "high"
	case strings.HasPrefix(action, "operator.") && !strings.Contains(action, "delete"):
		return "high"
	case strings.Contains(action, "delete"), strings.Contains(action, "permission"),
		strings.Contains(action, "kill"), strings.Contains(action, "password.reset"):
		return "critical"
	case strings.Contains(action, "update"), strings.Contains(action, "restart"),
		strings.Contains(action, "stop"), strings.Contains(action, "deploy"):
		return "high"
	default:
		return "normal"
	}
}

func detailMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"summary": "detail encoding failed"}
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return map[string]any{"value": string(encoded)}
	}
	return result
}

func (s *Server) handleInstance(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/instances/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	instanceID, action := parts[0], parts[1]
	if action == "plugins" {
		if request.Method == http.MethodPost {
			if !s.authorizeServerRequest(writer, request, "plugin.deploy") {
				return
			}
			s.handleInstancePluginUpload(writer, request, instanceID)
			return
		}
		if request.Method == http.MethodGet {
			if !s.authorizeServerRequest(writer, request, "plugin.view") {
				return
			}
			var result json.RawMessage
			err := s.callNode(request, "plugin.list", map[string]any{"instance_id": instanceID}, &result)
			if err != nil {
				writeError(writer, err)
				return
			}
			writeSuccess(writer, result)
			return
		}
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "GET, POST")
		return
	}
	switch action {
	case "start", "stop", "restart", "kill":
		messageType := "instance." + action
		if !s.authorizeServerRequest(writer, request, messageType) {
			return
		}
		var result json.RawMessage
		err := s.callNode(request, messageType, map[string]any{"instance_id": instanceID}, &result)
		s.record(request, messageType, instanceID, nil, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, result)
	case "command":
		if !s.authorizeServerRequest(writer, request, "console.command") {
			return
		}
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
			err = s.validateConsoleCommand(input.Command)
		}
		if err == nil {
			err = s.callNode(request, "console.command", map[string]any{
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
		if !s.authorizeServerRequest(writer, request, "console.read") {
			return
		}
		s.handleConsoleTicket(writer, request, instanceID)
	default:
		http.NotFound(writer, request)
	}
}

func (s *Server) validateConsoleCommand(command string) error {
	if errors.Is(consolecommand.Validate(command, s.config.Minecraft.ManageOperators), consolecommand.ErrOperatorManagement) {
		return apiError("COMMAND_FORBIDDEN", "OP 只能通过面板的玩家权限功能管理")
	}
	return nil
}

func (s *Server) handleConsoleTicket(writer http.ResponseWriter, request *http.Request, instanceID string) {
	var created struct {
		TicketID  string `json:"ticket_id"`
		Ticket    string `json:"ticket"`
		ExpiresAt string `json:"expires_at"`
		PublicURL string `json:"public_url"`
	}
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	sourceIP, sourceKnown := s.directAccessSourceIP(request)
	direct := sourceKnown && strings.TrimSpace(s.connections.Status(nodeID).PublicURL) != ""
	input := map[string]any{
		"scope": "console.read", "instance_id": instanceID, "ttl_seconds": 120,
	}
	if direct {
		input["source_ip"] = sourceIP
		input["session_id"] = hex.EncodeToString(currentSession(request).TokenHash)
	}
	err := s.callNode(request, "ticket.create", input, &created)
	if err != nil && direct {
		delete(input, "source_ip")
		delete(input, "session_id")
		direct = false
		err = s.callNode(request, "ticket.create", input, &created)
	}
	if err != nil {
		s.record(request, "console.subscribe", instanceID, nil, err)
		writeError(writer, err)
		return
	}
	endpoint := "/api/v1/ws/console?node_id=" + nodeID
	publicURL := ""
	if direct {
		publicURL = created.PublicURL
		if publicURL == "" {
			publicURL = s.connections.Status(nodeID).PublicURL
		}
	}
	if publicURL != "" {
		endpoint, err = daemon.PublicConsoleURL(publicURL)
	}
	s.record(request, "console.subscribe", instanceID, nil, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{
		"ticket_id": created.TicketID, "ticket": created.Ticket,
		"expires_at": created.ExpiresAt, "instance_id": instanceID,
		"websocket_url": endpoint, "direct": publicURL != "",
	})
}

func (s *Server) callNode(request *http.Request, messageType string, input, output any) error {
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	if nodeID == "" {
		return apiError("INVALID_REQUEST", "必须通过 node_id 指定目标节点")
	}
	return s.connections.Call(request.Context(), nodeID, messageType, input, output)
}
