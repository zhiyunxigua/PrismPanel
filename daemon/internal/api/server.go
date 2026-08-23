package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
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
	firewallservice "PrismPanel-daemon/internal/firewall"
	"PrismPanel-daemon/internal/model"
	pluginservice "PrismPanel-daemon/internal/plugins"
	"PrismPanel-daemon/internal/protocol"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/supervisor"
	"PrismPanel-daemon/internal/ticket"
	"github.com/gorilla/websocket"
)

var Version = "0.0.1"

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
	firewall    *firewallservice.Service
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
	firewallManager *firewallservice.Service,
	events *eventbus.Bus,
	logger *slog.Logger,
) *Server {
	api := &Server{
		config: cfg, secret: mainSecret, nodeID: nodeID, servers: servers, supervisor: manager,
		tickets: tickets, deployments: deployments, plugins: plugins, files: files, firewall: firewallManager,
		hub: newControlHub(), startedAt: time.Now().UTC(), logger: logger,
	}
	events.Subscribe(api.hub.broadcast)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", api.handleHealth)
	mux.HandleFunc("/", api.handleRoot)
	mux.HandleFunc("/api/v1/ws/control", api.handleControl)
	mux.HandleFunc("/api/v1/ws/console", api.handleConsole)
	mux.HandleFunc("/api/v1/ws/plugin", api.handlePlugin)
	mux.HandleFunc("/api/v1/plugins/deploy", api.handlePluginDeploy)
	mux.HandleFunc("/api/v1/plugins/config/deploy", api.handlePluginConfigDeploy)
	mux.HandleFunc("/api/v1/plugins/upload", api.handlePluginUpload)
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

func (s *Server) handleRoot(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodeID := html.EscapeString(strings.TrimSpace(s.nodeID))
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprintf(writer, `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Prism 守护进程</title>
<style>
:root{color-scheme:light dark;font-family:system-ui,-apple-system,"Segoe UI",sans-serif}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f4f6f4;color:#1d2721}
main{width:min(560px,calc(100%% - 48px));padding:40px;background:#fff;border:1px solid #d8dfd9;border-radius:8px;box-shadow:0 10px 30px #18251d14}
h1{margin:14px 0 8px;font-size:28px}p{margin:0 0 24px;color:#5e6a62;line-height:1.6}
.badge{display:inline-block;color:#27713c;font-size:12px;letter-spacing:.08em}
dl{margin:0;display:grid;grid-template-columns:100px 1fr;gap:10px;font-size:14px}
dt{color:#6c776f}dd{margin:0;overflow-wrap:anywhere}
code{font-family:ui-monospace,SFMono-Regular,Consolas,monospace;color:#214b2d}
@media(prefers-color-scheme:dark){body{background:#141815;color:#e8eee9}main{background:#1d241f;border-color:#354239}p,dt{color:#aab6ad}}
</style>
</head>
<body><main><span class="badge">PRISM DAEMON</span><h1>守护进程已启动</h1>
<p>请返回 PrismPanel，在节点管理中添加此守护进程。</p>
<dl><dt>节点 ID</dt><dd><code>%s</code></dd></dl></main></body>
</html>`, nodeID)
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

func (s *Server) capabilities() []string {
	capabilities := []string{
		"server.manage", "instance.lifecycle", "console", "deployment",
		"plugin.telemetry", "plugin.manage", "proxy.backends", "player.transfer", "operators.manage",
		"metrics", "files",
	}
	if s.firewall != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		status := s.firewall.Status(ctx)
		cancel()
		if status.Supported {
			capabilities = append(capabilities, "firewall.manage")
		}
	}
	return capabilities
}

func (s *Server) handleControl(writer http.ResponseWriter, request *http.Request) {
	clientSource := s.requestSourceIP(request)
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
	client := newControlClient(conn, clientSource)
	s.hub.add(client)
	defer s.hub.remove(client)
	go client.writeLoop()
	client.enqueue(protocol.Outgoing{
		Type: "auth.result", Success: boolPointer(true),
		Data: map[string]any{
			"node_id": s.nodeID, "version": Version, "protocol_version": ProtocolVersion,
			"public_url":   s.config.Server.PublicURL,
			"capabilities": s.capabilities(),
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
	data, err := s.executeFrom(client.source, incoming.Type, incoming.Data)
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
	return s.executeFrom("", messageType, raw)
}

func (s *Server) executeFrom(callerSource, messageType string, raw json.RawMessage) (any, error) {
	switch messageType {
	case "server.list":
		return s.servers.List(), nil
	case "metrics.snapshot":
		return s.supervisor.MetricsSnapshot(), nil
	case "plugin.list", "mods.list":
		var input struct {
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.plugins.List(input.InstanceID)
	case "plugin.enable", "plugin.disable", "plugin.uninstall", "mods.enable", "mods.disable", "mods.uninstall":
		var input pluginservice.OperationInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		switch messageType {
		case "plugin.enable", "mods.enable":
			return s.plugins.SetEnabled(input, true)
		case "plugin.disable", "mods.disable":
			return s.plugins.SetEnabled(input, false)
		default:
			return s.plugins.Uninstall(input)
		}
	case "proxy.backends.sync":
		var input struct {
			InstanceID string                    `json:"instance_id"`
			Revision   int64                     `json:"revision"`
			Servers    []supervisor.ProxyBackend `json:"servers"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return s.supervisor.SyncProxyBackends(ctx, input.InstanceID, supervisor.ProxyBackendCatalog{
			Revision: input.Revision,
			Servers:  input.Servers,
		})
	case "proxy.backends.status":
		var input struct {
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.supervisor.ProxySyncStatus(input.InstanceID)
	case "player.transfer":
		var input supervisor.PlayerTransferInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return map[string]any{}, s.supervisor.TransferPlayer(ctx, input)
	case "operators.replace":
		var input supervisor.OperatorSource
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return s.supervisor.ReplaceOperatorSource(ctx, input)
	case "operators.source.remove":
		var input struct {
			PanelID string `json:"panel_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return s.supervisor.RemoveOperatorSource(ctx, input.PanelID)
	case "operators.status":
		var input struct {
			PanelID string `json:"panel_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.supervisor.OperatorStatus(input.PanelID), nil
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
	case "firewall.status":
		if s.firewall == nil {
			return nil, apperr.New("FIREWALL_UNSUPPORTED", "当前守护进程未初始化防火墙服务")
		}
		ctx, cancel := firewallCommandContext()
		defer cancel()
		return s.firewall.Status(ctx), nil
	case "firewall.list":
		if s.firewall == nil {
			return nil, apperr.New("FIREWALL_UNSUPPORTED", "当前守护进程未初始化防火墙服务")
		}
		ctx, cancel := firewallCommandContext()
		defer cancel()
		return s.firewall.View(ctx), nil
	case "firewall.rule.create":
		if s.firewall == nil {
			return nil, apperr.New("FIREWALL_UNSUPPORTED", "当前守护进程未初始化防火墙服务")
		}
		var input firewallservice.CreateRuleInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		ctx, cancel := firewallCommandContext()
		defer cancel()
		return s.firewall.CreateRule(ctx, input)
	case "firewall.rule.update":
		if s.firewall == nil {
			return nil, apperr.New("FIREWALL_UNSUPPORTED", "当前守护进程未初始化防火墙服务")
		}
		var input struct {
			RuleID           string                    `json:"rule_id"`
			ExpectedRevision int64                     `json:"expected_revision"`
			Rule             firewallservice.RuleInput `json:"rule"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		ctx, cancel := firewallCommandContext()
		defer cancel()
		return s.firewall.UpdateRule(ctx, input.RuleID, firewallservice.UpdateRuleInput{
			ExpectedRevision: input.ExpectedRevision, Rule: input.Rule,
		})
	case "firewall.rule.delete":
		if s.firewall == nil {
			return nil, apperr.New("FIREWALL_UNSUPPORTED", "当前守护进程未初始化防火墙服务")
		}
		var input struct {
			RuleID           string `json:"rule_id"`
			ExpectedRevision int64  `json:"expected_revision"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		ctx, cancel := firewallCommandContext()
		defer cancel()
		return s.firewall.DeleteRule(ctx, input.RuleID, firewallservice.DeleteRuleInput{
			ExpectedRevision: input.ExpectedRevision,
		})
	case "firewall.system.configure":
		if s.firewall == nil {
			return nil, apperr.New("FIREWALL_UNSUPPORTED", "当前守护进程未初始化防火墙服务")
		}
		var input firewallservice.ConfigureSystemInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		ctx, cancel := firewallCommandContext()
		defer cancel()
		return s.firewall.ConfigureSystem(ctx, callerSource, input)
	case "firewall.grants.revoke_session":
		if s.firewall == nil {
			return nil, apperr.New("FIREWALL_UNSUPPORTED", "当前守护进程未初始化防火墙服务")
		}
		var input struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		s.tickets.RevokeSession(input.SessionID)
		ctx, cancel := firewallCommandContext()
		defer cancel()
		return map[string]any{}, s.firewall.RevokeSessionGrants(ctx, input.SessionID)
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
	case "deployment.plugin_config_sync.start":
		var input struct {
			ServerID    string   `json:"server_id"`
			Targets     []int    `json:"targets,omitempty"`
			Directories []string `json:"directories,omitempty"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.deployments.StartPluginConfigSync(input.ServerID, input.Targets, input.Directories)
	case "config_sync.detect":
		var input struct {
			ServerID string `json:"server_id"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, invalidJSON(err)
		}
		return s.deployments.DetectConfigSyncDirs(input.ServerID)
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
		Scope           string   `json:"scope"`
		InstanceID      string   `json:"instance_id"`
		TTLSeconds      int      `json:"ttl_seconds"`
		SHA256          string   `json:"sha256,omitempty"`
		ExpectedVersion string   `json:"expected_version,omitempty"`
		Size            int64    `json:"size,omitempty"`
		ResourceType    string   `json:"resource_type,omitempty"`
		ResourceID      string   `json:"resource_id,omitempty"`
		Path            string   `json:"path,omitempty"`
		Paths           []string `json:"paths,omitempty"`
		PathPrefix      bool     `json:"path_prefix,omitempty"`
		Method          string   `json:"method,omitempty"`
		OperationID     string   `json:"operation_id,omitempty"`
		Overwrite       bool     `json:"overwrite,omitempty"`
		Recursive       bool     `json:"recursive,omitempty"`
		Chunked         bool     `json:"chunked,omitempty"`
		SourceIP        string   `json:"source_ip,omitempty"`
		SessionID       string   `json:"session_id,omitempty"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, invalidJSON(err)
	}
	if (input.SourceIP != "" || input.SessionID != "") &&
		input.Scope != "console.read" && !strings.HasPrefix(input.Scope, "file.") {
		return nil, apperr.New("INVALID_TICKET", "当前票据范围不支持直接访问授权")
	}
	if input.Scope == "plugin.upload" {
		if _, err := s.supervisor.Get(input.InstanceID); err != nil {
			return nil, err
		}
		if input.Size <= 0 || input.Size > maxInstancePluginUploadSize {
			return nil, apperr.New("FILE_TOO_LARGE", "插件 JAR 超过上传限制")
		}
		if input.TTLSeconds == 0 {
			input.TTLSeconds = 300
		}
		created, err := s.tickets.CreateRestricted(ticket.RestrictedOptions{
			Scope: input.Scope, ResourceType: "instance", ResourceID: input.InstanceID,
			Path: ".", Method: http.MethodPost, MaxBytes: input.Size, SHA256: input.SHA256,
			AllowOverwrite: input.Overwrite, TTL: time.Duration(input.TTLSeconds) * time.Second, MaxUses: 1,
		})
		if err != nil {
			return nil, err
		}
		if err := s.prepareDirectTicket(created, input.SourceIP, input.SessionID); err != nil {
			return nil, err
		}
		return map[string]any{
			"ticket_id": created.ID, "ticket": created.Token, "expires_at": created.ExpiresAt,
			"scope": created.Scope, "instance_id": input.InstanceID,
			"public_url": s.config.Server.PublicURL, "upload_path": "/api/v1/plugins/upload",
		}, nil
	}
	if input.Scope == "plugin.deploy" || input.Scope == "plugin.config.deploy" {
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
		uploadPath := "/api/v1/plugins/deploy"
		if input.Scope == "plugin.config.deploy" {
			uploadPath = "/api/v1/plugins/config/deploy"
		}
		return map[string]any{
			"ticket_id": created.ID, "ticket": created.Token, "expires_at": created.ExpiresAt,
			"scope": created.Scope, "instance_id": created.InstanceID,
			"public_url": s.config.Server.PublicURL, "upload_path": uploadPath,
		}, nil
	}
	if strings.HasPrefix(input.Scope, "file.") {
		allowed := map[string]string{
			"file.list": http.MethodPost, "file.read": http.MethodGet,
			"file.edit": http.MethodPut, "file.upload": http.MethodPost,
			"file.import":   http.MethodPost,
			"file.download": http.MethodGet, "file.create": http.MethodPost,
			"file.move": http.MethodPost, "file.copy": http.MethodPost,
			"file.archive": http.MethodPost, "file.delete": http.MethodPost,
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
		if input.Scope == "file.upload" && input.Chunked {
			chunks := (input.Size + fileservice.UploadChunkSize - 1) / fileservice.UploadChunkSize
			if chunks < 1 {
				chunks = 1
			}
			maxUses = int(chunks*4 + 1)
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
			ExpectedVersion: input.ExpectedVersion,
			AllowOverwrite:  input.Overwrite, AllowRecursive: input.Recursive,
			TTL: time.Duration(input.TTLSeconds) * time.Second, MaxUses: maxUses,
			ClientIP: input.SourceIP, SessionID: input.SessionID,
		})
		if err != nil {
			return nil, err
		}
		if err := s.prepareDirectTicket(created, input.SourceIP, input.SessionID); err != nil {
			return nil, err
		}
		return map[string]any{
			"ticket_id": created.ID, "ticket": created.Token, "expires_at": created.ExpiresAt,
			"scope": created.Scope, "resource_type": created.ResourceType,
			"resource_id": created.ResourceID, "path": created.Path, "paths": created.Paths,
			"public_url": s.config.Server.PublicURL, "max_bytes": created.MaxBytes,
			"max_uses": created.MaxUses, "chunk_size": fileservice.UploadChunkSize,
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
	created, err := s.tickets.CreateWithOptions(input.Scope, input.InstanceID, time.Duration(input.TTLSeconds)*time.Second, 1, ticket.TicketOptions{
		ClientIP: input.SourceIP, SessionID: input.SessionID,
	})
	if err != nil {
		return nil, err
	}
	if err := s.prepareDirectTicket(created, input.SourceIP, input.SessionID); err != nil {
		return nil, err
	}
	return map[string]any{
		"ticket_id": created.ID, "ticket": created.Token, "expires_at": created.ExpiresAt,
		"scope": created.Scope, "instance_id": created.InstanceID,
		"public_url": s.config.Server.PublicURL,
	}, nil
}

func (s *Server) prepareDirectTicket(created ticket.Ticket, source, sessionID string) error {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	if s.firewall == nil {
		s.tickets.Revoke(created.ID)
		return apperr.New("FIREWALL_UNSUPPORTED", "当前守护进程未初始化防火墙服务")
	}
	ctx, cancel := firewallCommandContext()
	defer cancel()
	if err := s.firewall.GrantDirectAccess(ctx, source, strings.TrimSpace(sessionID), created.ID); err != nil {
		s.tickets.Revoke(created.ID)
		return err
	}
	return nil
}

func (s *Server) handleConsole(writer http.ResponseWriter, request *http.Request) {
	clientSource := s.requestSourceIP(request)
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
	if _, err := s.tickets.ConsumeFrom(auth.Ticket, "console.read", auth.InstanceID, clientSource); err != nil {
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
	if len(history) > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(buildConsoleSnapshot(history, auth.AfterSequence)); err != nil {
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

func firewallCommandContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func boolPointer(value bool) *bool {
	return &value
}
