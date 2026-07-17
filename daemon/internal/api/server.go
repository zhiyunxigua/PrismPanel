package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/deployment"
	"PrismPanel-daemon/internal/eventbus"
	fileservice "PrismPanel-daemon/internal/files"
	"PrismPanel-daemon/internal/model"
	pluginservice "PrismPanel-daemon/internal/plugins"
	"PrismPanel-daemon/internal/protocol"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/supervisor"
	"PrismPanel-daemon/internal/ticket"
	"github.com/gorilla/websocket"
)

const Version = "dev"
const ProtocolVersion = "1"

type Server struct {
	config      config.Config
	secret      string
	nodeID      string
	servers     *serverservice.Service
	supervisor  *supervisor.Manager
	tickets     *ticket.Manager
	deployments *deployment.Manager
	plugins     *pluginservice.Service
	files       *fileservice.Service
	hub         *controlHub
	http        *http.Server
	startedAt   time.Time
	logger      *slog.Logger
}

func NewServer(
	cfg config.Config,
	mainSecret string,
	nodeID string,
	servers *serverservice.Service,
	manager *supervisor.Manager,
	tickets *ticket.Manager,
	deployments *deployment.Manager,
	plugins *pluginservice.Service,
	files *fileservice.Service,
	events *eventbus.Bus,
	logger *slog.Logger,
) *Server {
	api := &Server{
		config: cfg, secret: mainSecret, nodeID: nodeID, servers: servers, supervisor: manager,
		tickets: tickets, deployments: deployments, plugins: plugins, files: files,
		hub: newControlHub(), startedAt: time.Now().UTC(), logger: logger,
	}
	events.Subscribe(api.hub.broadcast)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", api.handleHealth)
	mux.HandleFunc("/api/v1/ws/control", api.handleControl)
	mux.HandleFunc("/api/v1/ws/console", api.handleConsole)
	mux.HandleFunc("/api/v1/ws/plugin", api.handlePlugin)
	mux.HandleFunc("/api/v1/plugins/deploy", api.handlePluginDeploy)
	mux.HandleFunc("/api/v1/files/", api.handleFiles)
	api.http = &http.Server{
		Addr:              net.JoinHostPort(cfg.Server.Listen, fmt.Sprintf("%d", cfg.Server.Port)),
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       75 * time.Second,
	}
	return api
}

func (s *Server) ListenAndServe() error {
	if s.config.SSL.Enabled {
		return s.http.ListenAndServeTLS(s.config.SSL.CertFile, s.config.SSL.KeyFile)
	}
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.hub.close()
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"status": "ok", "version": Version, "uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		"panel_connections": s.hub.count(),
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

var upgrader = websocket.Upgrader{
	ReadBufferSize: 4096, WriteBufferSize: 4096,
	CheckOrigin: func(_ *http.Request) bool { return true },
}

func secretsEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (s *Server) handleControl(writer http.ResponseWriter, request *http.Request) {
	conn, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(1024 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth protocol.Incoming
	if err := conn.ReadJSON(&auth); err != nil || auth.Type != "auth" || !secretsEqual(auth.Token, s.secret) {
		_ = conn.WriteJSON(protocol.Outgoing{
			Type: "auth.result", Success: boolPointer(false),
			Error: apperr.New("UNAUTHENTICATED", "节点令牌无效"),
		})
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	})
	client := newControlClient(conn)
	s.hub.add(client)
	defer s.hub.remove(client)
	go client.writeLoop()
	client.enqueue(protocol.Outgoing{
		Type: "auth.result", Success: boolPointer(true),
		Data: map[string]any{
			"node_id": s.nodeID, "version": Version, "protocol_version": ProtocolVersion,
			"public_url":   s.config.Server.PublicURL,
			"capabilities": []string{"server.manage", "instance.lifecycle", "console", "deployment", "plugin.telemetry", "plugin.manage", "metrics", "files"},
		},
	})
	client.enqueue(s.supervisor.SnapshotEvent())

	for {
		var incoming protocol.Incoming
		if err := conn.ReadJSON(&incoming); err != nil {
			return
		}
		if incoming.RequestID == "" {
			client.enqueue(protocol.Failure("", apperr.New("INVALID_REQUEST", "request_id 不能为空")))
			continue
		}
		go s.handleControlRequest(client, incoming)
	}
}

func (s *Server) handleControlRequest(client *controlClient, incoming protocol.Incoming) {
	data, err := s.execute(incoming.Type, incoming.Data)
	if err != nil {
		apiError := apperr.From(err)
		if apiError.Code == "INTERNAL" {
			s.logger.Error("control request failed", "type", incoming.Type, "request_id", incoming.RequestID, "error", err)
		}
		client.enqueue(protocol.Failure(incoming.RequestID, apiError))
		return
	}
	client.enqueue(protocol.Response(incoming.RequestID, data))
}

func (s *Server) execute(messageType string, raw json.RawMessage) (any, error) {
	switch messageType {
	case "server.list":
		return s.servers.List(), nil
	case "metrics.snapshot":
		return s.supervisor.MetricsSnapshot(), nil
	case "plugin.list":
		var input struct {
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.plugins.List(input.InstanceID)
	case "plugin.enable", "plugin.disable", "plugin.uninstall":
		var input pluginservice.OperationInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		switch messageType {
		case "plugin.enable":
			return s.plugins.SetEnabled(input, true)
		case "plugin.disable":
			return s.plugins.SetEnabled(input, false)
		default:
			return s.plugins.Uninstall(input)
		}
	case "server.get":
		var input struct {
			ServerID string `json:"server_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.servers.Get(input.ServerID)
	case "server.create":
		var input model.ServerConfig
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		warnings, err := s.servers.Create(input)
		if err == nil {
			s.hub.broadcast(s.supervisor.SnapshotEvent())
		}
		return map[string]any{"warnings": warnings}, err
	case "server.update":
		var input model.ServerConfig
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		warnings, err := s.servers.Update(input.ServerID, input)
		if err == nil {
			s.hub.broadcast(s.supervisor.SnapshotEvent())
		}
		return map[string]any{"warnings": warnings}, err
	case "server.delete":
		var input struct {
			ServerID string `json:"server_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		err := s.servers.Delete(input.ServerID)
		if err == nil {
			s.hub.broadcast(s.supervisor.SnapshotEvent())
		}
		return map[string]any{}, err
	case "instance.start", "instance.stop", "instance.restart", "instance.kill":
		var input struct {
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		var err error
		switch messageType {
		case "instance.start":
			err = s.supervisor.Start(input.InstanceID)
		case "instance.stop":
			err = s.supervisor.Stop(input.InstanceID)
		case "instance.restart":
			err = s.supervisor.Restart(input.InstanceID)
		case "instance.kill":
			err = s.supervisor.Kill(input.InstanceID)
		}
		if err != nil {
			return nil, err
		}
		return s.supervisor.Get(input.InstanceID)
	case "console.command":
		var input struct {
			InstanceID string `json:"instance_id"`
			Command    string `json:"command"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return map[string]any{}, s.supervisor.Command(input.InstanceID, input.Command)
	case "ticket.create":
		return s.createTicket(raw)
	case "ticket.revoke":
		var input struct {
			TicketID string `json:"ticket_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		s.tickets.Revoke(input.TicketID)
		return map[string]any{}, nil
	case "deployment.start":
		var input struct {
			ServerID string `json:"server_id"`
			Targets  []int  `json:"targets,omitempty"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.deployments.Start(input.ServerID, input.Targets)
	case "deployment.get":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.deployments.Get(input.TaskID)
	case "deployment.active":
		var input struct {
			ServerID string `json:"server_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.deployments.Active(input.ServerID)
	case "deployment.cancel", "deployment.force_stop":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.deployments.Cancel(input.TaskID, messageType == "deployment.force_stop")
	default:
		return nil, apperr.New("UNKNOWN_COMMAND", "不支持的管理命令")
	}
}

func (s *Server) createTicket(raw json.RawMessage) (any, error) {
	var input struct {
		Scope        string   `json:"scope"`
		InstanceID   string   `json:"instance_id"`
		TTLSeconds   int      `json:"ttl_seconds"`
		SHA256       string   `json:"sha256,omitempty"`
		Size         int64    `json:"size,omitempty"`
		ResourceType string   `json:"resource_type,omitempty"`
		ResourceID   string   `json:"resource_id,omitempty"`
		Path         string   `json:"path,omitempty"`
		Paths        []string `json:"paths,omitempty"`
		PathPrefix   bool     `json:"path_prefix,omitempty"`
		Method       string   `json:"method,omitempty"`
		OperationID  string   `json:"operation_id,omitempty"`
		Overwrite    bool     `json:"overwrite,omitempty"`
		Recursive    bool     `json:"recursive,omitempty"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, invalidJSON(err)
	}
	if input.Scope == "plugin.deploy" {
		if _, err := s.servers.Get(input.InstanceID); err != nil {
			return nil, err
		}
		if input.TTLSeconds == 0 {
			input.TTLSeconds = 300
		}
		created, err := s.tickets.CreateUpload(input.Scope, input.InstanceID, input.SHA256, input.Size,
			time.Duration(input.TTLSeconds)*time.Second)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ticket_id": created.ID, "ticket": created.Token, "expires_at": created.ExpiresAt,
			"scope": created.Scope, "instance_id": created.InstanceID,
			"public_url": s.config.Server.PublicURL, "upload_path": "/api/v1/plugins/deploy",
		}, nil
	}
	if strings.HasPrefix(input.Scope, "file.") {
		allowed := map[string]string{
			"file.list": http.MethodPost, "file.read": http.MethodGet,
			"file.edit": http.MethodPut, "file.upload": http.MethodPost,
			"file.import":   http.MethodPost,
			"file.download": http.MethodGet, "file.create": http.MethodPost,
			"file.move": http.MethodPost, "file.delete": http.MethodPost,
		}
		method, exists := allowed[input.Scope]
		if !exists || (input.Method != "" && !strings.EqualFold(input.Method, method)) {
			return nil, apperr.New("INVALID_TICKET", "文件凭证范围或请求方法无效")
		}
		if input.ResourceType == "instance" {
			if _, err := s.supervisor.Get(input.ResourceID); err != nil {
				return nil, err
			}
		} else if input.ResourceType == "image" {
			server, err := s.servers.Get(input.ResourceID)
			if err != nil {
				return nil, err
			}
			if server.Type != "mirror" {
				return nil, apperr.New("INVALID_STATE", "目标服务器没有镜像源")
			}
		} else {
			return nil, apperr.New("INVALID_TICKET", "文件凭证资源类型无效")
		}
		if input.TTLSeconds == 0 {
			input.TTLSeconds = 120
		}
		maxUses := 1
		if input.Scope == "file.list" {
			maxUses = 64
			input.PathPrefix = true
		}
		maxBytes := input.Size
		if input.Scope == "file.edit" || input.Scope == "file.read" {
			maxBytes = s.config.Files.MaxEditFileSize
		}
		if input.Scope == "file.upload" || input.Scope == "file.import" {
			if maxBytes < 0 || maxBytes > s.config.Files.MaxUploadFileSize {
				return nil, apperr.New("FILE_TOO_LARGE", "上传文件超过节点限制")
			}
		}
		created, err := s.tickets.CreateRestricted(ticket.RestrictedOptions{
			Scope: input.Scope, ResourceType: input.ResourceType, ResourceID: input.ResourceID,
			Path: input.Path, Paths: input.Paths, PathPrefix: input.PathPrefix, Method: method,
			OperationID: input.OperationID, MaxBytes: maxBytes, SHA256: input.SHA256,
			AllowOverwrite: input.Overwrite, AllowRecursive: input.Recursive,
			TTL: time.Duration(input.TTLSeconds) * time.Second, MaxUses: maxUses,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"ticket_id": created.ID, "ticket": created.Token, "expires_at": created.ExpiresAt,
			"scope": created.Scope, "resource_type": created.ResourceType,
			"resource_id": created.ResourceID, "path": created.Path, "paths": created.Paths,
			"public_url": s.config.Server.PublicURL, "max_bytes": created.MaxBytes,
		}, nil
	}
	if input.Scope != "console.read" {
		return nil, apperr.New("INVALID_TICKET", "当前仅支持 console.read 临时凭证")
	}
	if _, err := s.supervisor.Get(input.InstanceID); err != nil {
		return nil, err
	}
	if input.TTLSeconds == 0 {
		input.TTLSeconds = 120
	}
	created, err := s.tickets.Create(input.Scope, input.InstanceID, time.Duration(input.TTLSeconds)*time.Second, 1)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ticket_id": created.ID, "ticket": created.Token, "expires_at": created.ExpiresAt,
		"scope": created.Scope, "instance_id": created.InstanceID,
		"public_url": s.config.Server.PublicURL,
	}, nil
}

func (s *Server) handleConsole(writer http.ResponseWriter, request *http.Request) {
	conn, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(64 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth struct {
		Type          string `json:"type"`
		Ticket        string `json:"ticket"`
		InstanceID    string `json:"instance_id"`
		AfterSequence uint64 `json:"after_sequence"`
	}
	if err := conn.ReadJSON(&auth); err != nil || auth.Type != "auth" {
		_ = conn.WriteJSON(map[string]any{
			"type": "auth.result", "success": false,
			"error": apperr.New("UNAUTHENTICATED", "缺少控制台临时凭证"),
		})
		return
	}
	if _, err := s.tickets.Consume(auth.Ticket, "console.read", auth.InstanceID); err != nil {
		_ = conn.WriteJSON(map[string]any{
			"type": "auth.result", "success": false, "error": apperr.From(err),
		})
		return
	}
	history, lines, cancel, err := s.supervisor.Subscribe(auth.InstanceID, auth.AfterSequence)
	if err != nil {
		_ = conn.WriteJSON(map[string]any{
			"type": "auth.result", "success": false, "error": apperr.From(err),
		})
		return
	}
	defer cancel()
	_ = conn.SetReadDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteJSON(map[string]any{"type": "auth.result", "success": true}); err != nil {
		return
	}
	for _, line := range history {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(line); err != nil {
			return
		}
	}
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case line, open := <-lines:
			if !open {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteJSON(line); err != nil {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func invalidJSON(err error) error {
	return apperr.Wrap("INVALID_REQUEST", "请求数据格式无效", err)
}

func boolPointer(value bool) *bool {
	return &value
}
