package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"PrismPanel/internal/auth"
	"PrismPanel/internal/config"
	"PrismPanel/internal/daemon"
	panelmetrics "PrismPanel/internal/metrics"
	"PrismPanel/internal/netgames"
	panelnodes "PrismPanel/internal/nodes"
	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/store"
)

type Server struct {
	config      config.Config
	connections *daemon.Manager
	auth        *auth.Service
	store       *store.Store
	nodes       *panelnodes.Service
	metrics     *panelmetrics.Store
	plugins     *panelplugins.Repository
	netGames    *netgames.Service
	fileProxies *fileProxyStore
	http        *http.Server
	logger      *slog.Logger
	proxySyncMu sync.Mutex
}

type response struct {
	Success   bool   `json:"success"`
	Data      any    `json:"data,omitempty"`
	Error     any    `json:"error,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func NewServer(
	cfg config.Config,
	authService *auth.Service,
	repository *store.Store,
	nodeService *panelnodes.Service,
	connectionManager *daemon.Manager,
	metricStore *panelmetrics.Store,
	pluginRepository *panelplugins.Repository,
	netGameService *netgames.Service,
	logger *slog.Logger,
) *Server {
	server := &Server{
		config: cfg, auth: authService, store: repository, connections: connectionManager,
		nodes: nodeService, metrics: metricStore, plugins: pluginRepository, netGames: netGameService,
		fileProxies: newFileProxyStore(), logger: logger,
	}
	connectionManager.AddStatusCallback(func(_ string, status daemon.RuntimeStatus) {
		if status.State == "ONLINE" {
			go server.reconcileAllProxies(context.Background())
		}
	})
	go server.reconcileAllProxies(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/status", server.handleAuthStatus)
	mux.HandleFunc("/api/v1/auth/login", server.handleLogin)
	mux.HandleFunc("/api/v1/auth/logout", server.requireAuth(server.handleLogout))
	mux.HandleFunc("/api/v1/auth/session", server.requireAuth(server.handleSession))
	mux.HandleFunc("/api/v1/auth/password", server.requireAuth(server.handlePassword))
	mux.HandleFunc("/api/v1/users", server.requireAuth(server.handleUsers))
	mux.HandleFunc("/api/v1/users/", server.requireAuth(server.handleUser))
	mux.HandleFunc("/api/v1/user-groups", server.requireAuth(server.handleUserGroups))
	mux.HandleFunc("/api/v1/user-groups/", server.requireAuth(server.handleUserGroup))
	mux.HandleFunc("/api/v1/permissions", server.requireSuperAdmin(server.handlePermissions))
	mux.HandleFunc("/api/v1/audit", server.requirePermission("audit.view", server.handleAudit))
	mux.HandleFunc("/api/v1/health", server.requireAuth(server.handleHealth))
	mux.HandleFunc("/api/v1/dashboard", server.requireAuth(server.handleDashboard))
	mux.HandleFunc("/api/v1/nodes", server.requireAuth(server.handleNodes))
	mux.HandleFunc("/api/v1/nodes/", server.requireAuth(server.handleNode))
	mux.HandleFunc("/api/v1/servers", server.requireAuth(server.handleServers))
	mux.HandleFunc("/api/v1/servers/", server.requireAuth(server.handleServer))
	mux.HandleFunc("/api/v1/instances/", server.requireAuth(server.handleInstance))
	mux.HandleFunc("/api/v1/deployments/", server.requireAuth(server.handleDeployment))
	mux.HandleFunc("/api/v1/plugins", server.requireAuth(server.handlePlugins))
	mux.HandleFunc("/api/v1/plugins/", server.requireAuth(server.handlePluginArtifact))
	mux.HandleFunc("/api/v1/plugins/rescan", server.requireAuth(server.handlePluginRescan))
	mux.HandleFunc("/api/v1/proxy-sync-rules", server.requireAuth(server.handleProxySyncRules))
	mux.HandleFunc("/api/v1/plugin-deploy-preferences", server.requireAuth(server.handlePluginDeployPreferences))
	mux.HandleFunc("/api/v1/net-games/settings", server.requireSuperAdmin(server.handleNetGameSettings))
	mux.HandleFunc("/api/v1/net-games/account", server.requireSuperAdmin(server.handleNetGameAccount))
	mux.HandleFunc("/api/v1/net-games/account/verify", server.requireSuperAdmin(server.handleNetGameAccountVerify))
	mux.HandleFunc("/api/v1/net-games/collect", server.requireSuperAdmin(server.handleNetGameCollect))
	mux.HandleFunc("/api/v1/net-games/collector-status", server.requireSuperAdmin(server.handleNetGameCollectorStatus))
	mux.HandleFunc("/api/v1/net-games/series", server.requirePermission("dashboard.view", server.handleNetGameSeries))
	mux.HandleFunc("/api/v1/net-games/", server.requirePermission("dashboard.view", server.handleNetGame))
	mux.HandleFunc("/api/v1/user/preferences/net-games", server.requirePermission("dashboard.view", server.handleNetGamePreference))
	mux.HandleFunc("/api/v1/players/transfer", server.requireAuth(server.handlePlayerTransfer))
	mux.HandleFunc("/api/v1/files/authorize", server.requireAuth(server.handleFileAuthorize))
	mux.HandleFunc("/api/v1/files/export", server.requireAuth(server.handleFileExport))
	mux.HandleFunc("/api/v1/files/proxy/", server.requireAuth(server.handleFileProxy))
	mux.HandleFunc("/api/v1/ws/console", server.requirePermission("console.read", server.handleConsoleProxy))
	mux.Handle("/", frontendHandler(cfg.Frontend.Directory))
	server.http = &http.Server{
		Addr: net.JoinHostPort(cfg.Server.Listen, fmt.Sprintf("%d", cfg.Server.Port)),
		Handler: server.withRequestID(server.checkOrigin(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			writer.Header().Set("Referrer-Policy", "same-origin")
			writer.Header().Set("X-Frame-Options", "DENY")
			mux.ServeHTTP(writer, request)
		}))),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       75 * time.Second,
	}
	return server
}

func (s *Server) ListenAndServe() error {
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) handleHealth(writer http.ResponseWriter, _ *http.Request) {
	writeSuccess(writer, map[string]any{"panel": "ok"})
}

func frontendHandler(directory string) http.Handler {
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "frontend directory is unavailable", http.StatusNotFound)
		})
	}
	root := filepath.Clean(directory)
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") {
			http.NotFound(writer, request)
			return
		}
		cleanPath := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
		target := filepath.Join(root, filepath.FromSlash(cleanPath))
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			files.ServeHTTP(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(root, "index.html"))
	})
}

func writeSuccess(writer http.ResponseWriter, data any) {
	writeJSON(writer, http.StatusOK, response{Success: true, Data: data})
}

func writeError(writer http.ResponseWriter, err error) {
	writeRequestError(writer, err)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	if payload, ok := value.(response); ok {
		payload.RequestID = writer.Header().Get("X-Request-ID")
		value = payload
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func readBody(request *http.Request) (json.RawMessage, error) {
	defer request.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(request.Body, 1024*1024+1))
	if err != nil {
		return nil, &daemon.APIError{Code: "INVALID_REQUEST", Message: "请求体读取失败"}
	}
	if len(contents) > 1024*1024 {
		return nil, &daemon.APIError{Code: "INVALID_REQUEST", Message: "请求体超过 1 MiB 限制"}
	}
	if !json.Valid(contents) {
		return nil, &daemon.APIError{Code: "INVALID_REQUEST", Message: "请求体不是有效 JSON"}
	}
	return contents, nil
}
