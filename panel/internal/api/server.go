package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"PrismPanel/internal/audit"
	"PrismPanel/internal/config"
	"PrismPanel/internal/daemon"
)

type Server struct {
	config config.Config
	daemon *daemon.Client
	audit  *audit.Logger
	http   *http.Server
	logger *slog.Logger
}

type response struct {
	Success bool `json:"success"`
	Data    any  `json:"data,omitempty"`
	Error   any  `json:"error,omitempty"`
}

func NewServer(cfg config.Config, client *daemon.Client, auditLogger *audit.Logger, logger *slog.Logger) *Server {
	server := &Server{config: cfg, daemon: client, audit: auditLogger, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", server.handleHealth)
	mux.HandleFunc("/api/v1/servers", server.handleServers)
	mux.HandleFunc("/api/v1/servers/", server.handleServer)
	mux.HandleFunc("/api/v1/instances/", server.handleInstance)
	mux.HandleFunc("/api/v1/deployments/", server.handleDeployment)
	mux.HandleFunc("/api/v1/ws/console", server.handleConsoleProxy)
	mux.Handle("/", frontendHandler(cfg.Frontend.Directory))
	server.http = &http.Server{
		Addr: net.JoinHostPort(cfg.Server.Listen, fmt.Sprintf("%d", cfg.Server.Port)),
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Prism-Test-Mode", "true")
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			mux.ServeHTTP(writer, request)
		}),
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
	connected, publicURL, version := s.daemon.Status()
	writeSuccess(writer, map[string]any{
		"panel": "ok", "daemon_connected": connected,
		"daemon_version": version, "daemon_public_url": publicURL,
	})
}

func frontendHandler(directory string) http.Handler {
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "frontend directory is unavailable", http.StatusNotFound)
		})
	}
	return http.FileServer(http.Dir(filepath.Clean(directory)))
}

func writeSuccess(writer http.ResponseWriter, data any) {
	writeJSON(writer, http.StatusOK, response{Success: true, Data: data})
}

func writeError(writer http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var apiError *daemon.APIError
	switch {
	case errors.Is(err, daemon.ErrDisconnected):
		status = http.StatusServiceUnavailable
	case errors.As(err, &apiError):
		switch apiError.Code {
		case "SERVER_NOT_FOUND", "INSTANCE_NOT_FOUND":
			status = http.StatusNotFound
		case "INSTANCE_BUSY", "PORT_CONFLICT", "SERVER_ID_CONFLICT", "DEPLOYMENT_ALREADY_RUNNING":
			status = http.StatusConflict
		case "INTERNAL", "CONFIG_WRITE_FAILED":
			status = http.StatusInternalServerError
		}
	default:
		status = http.StatusInternalServerError
		apiError = &daemon.APIError{Code: "INTERNAL", Message: "面板内部错误"}
	}
	writeJSON(writer, status, response{Success: false, Error: apiError})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
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

func actor(request *http.Request) string {
	value := strings.TrimSpace(request.Header.Get("X-Prism-Actor"))
	if value == "" {
		return "test-user"
	}
	return value
}
