package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	fileservice "PrismPanel-daemon/internal/files"
	"PrismPanel-daemon/internal/model"
	"PrismPanel-daemon/internal/supervisor"
	"PrismPanel-daemon/internal/ticket"
)

// newFilesTestServer 构造一个可用的文件 API Server：
// 一个 standalone 实例（workspace 指向临时目录）+ 文件服务 + 票据管理器。
func newFilesTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	workspace := t.TempDir()
	serverConfig := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "files-test", Name: "Files Test",
		Workspace: workspace, Port: 25565,
		Process: model.ProcessConfig{
			StartCommand: "java -jar server.jar", StopCommand: "stop", StopTimeoutSeconds: 30,
		},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := supervisor.NewManager(config.Default(), &eventbus.Bus{}, []model.ServerConfig{serverConfig})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	files := fileservice.NewService(nil, manager, nil,
		cfg.Files.MaxEditFileSize, cfg.Files.MaxUploadFileSize,
		cfg.Files.MaxExtractedSize, cfg.Files.MaxArchiveDownloadSize,
		cfg.Files.MaxConcurrentTransfers)
	server := &Server{
		config: cfg, supervisor: manager, tickets: ticket.NewManager(), files: files,
		hub: newControlHub(), logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return server, workspace
}

// createFileTicket 模拟 panel 的 ticket.create 命令为文件操作签发受限票据。
func createFileTicket(t *testing.T, server *Server, scope, path string, paths []string, method string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"scope": scope, "resource_type": "instance", "resource_id": "files-test",
		"path": path, "paths": paths, "method": method, "ttl_seconds": 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := server.createTicket(raw)
	if err != nil {
		t.Fatalf("create %s ticket: %v", scope, err)
	}
	token, ok := issued.(map[string]any)["ticket"].(string)
	if !ok || token == "" {
		t.Fatalf("ticket response missing token: %#v", issued)
	}
	return token
}

func writeWorkspaceFile(t *testing.T, workspace, relative, content string) {
	t.Helper()
	target := filepath.Join(workspace, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestFileTargetPrefersQueryPathOverHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://daemon/api/v1/files/content?path=%E6%B5%8B%E8%AF%95%2F%E6%96%87%E4%BB%B6.txt", nil)
	request.Header.Set(resourceTypeHeader, "instance")
	request.Header.Set(resourceIDHeader, "instance-1")
	request.Header.Set(filePathHeader, "header-fallback.txt")

	target, relative := fileTarget(request)
	if target.Type != "instance" || target.ID != "instance-1" {
		t.Fatalf("target = %#v", target)
	}
	if relative != "测试/文件.txt" {
		t.Fatalf("relative = %q, want 测试/文件.txt", relative)
	}
}

func TestFileTargetFallsBackToHeaderPath(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://daemon/api/v1/files/content", nil)
	request.Header.Set(resourceTypeHeader, "instance")
	request.Header.Set(resourceIDHeader, "instance-1")
	request.Header.Set(filePathHeader, "测试/文件.txt")

	_, relative := fileTarget(request)
	if relative != "测试/文件.txt" {
		t.Fatalf("relative = %q, want 测试/文件.txt", relative)
	}
}

func TestFileReadChinesePathViaQuery(t *testing.T) {
	server, workspace := newFilesTestServer(t)
	writeWorkspaceFile(t, workspace, "测试/文件.txt", "你好，Prism")

	token := createFileTicket(t, server, "file.read", "测试/文件.txt", []string{"测试/文件.txt"}, http.MethodGet)
	target := "http://daemon/api/v1/files/content?path=" + url.QueryEscape("测试/文件.txt")
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(resourceTypeHeader, "instance")
	request.Header.Set(resourceIDHeader, "files-test")

	recorder := httptest.NewRecorder()
	server.handleFiles(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Content string `json:"content"`
			Path    string `json:"path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Success || payload.Data.Content != "你好，Prism" || payload.Data.Path != "测试/文件.txt" {
		t.Fatalf("unexpected read result: %s", recorder.Body.String())
	}
}

func TestFileReadChinesePathViaHeaderFallback(t *testing.T) {
	server, workspace := newFilesTestServer(t)
	writeWorkspaceFile(t, workspace, "测试/文件.txt", "你好，Prism")

	token := createFileTicket(t, server, "file.read", "测试/文件.txt", []string{"测试/文件.txt"}, http.MethodGet)
	request := httptest.NewRequest(http.MethodGet, "http://daemon/api/v1/files/content", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(resourceTypeHeader, "instance")
	request.Header.Set(resourceIDHeader, "files-test")
	request.Header.Set(filePathHeader, "测试/文件.txt")

	recorder := httptest.NewRecorder()
	server.handleFiles(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "你好，Prism") {
		t.Fatalf("unexpected read result: %s", recorder.Body.String())
	}
}

func TestFileDeleteChinesePathViaQuery(t *testing.T) {
	server, workspace := newFilesTestServer(t)
	writeWorkspaceFile(t, workspace, "测试/目录/文件.txt", "to be deleted")

	token := createFileTicket(t, server, "file.delete", "测试/目录/文件.txt", []string{"测试/目录/文件.txt"}, http.MethodPost)
	target := "http://daemon/api/v1/files/delete?path=" + url.QueryEscape("测试/目录/文件.txt")
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"paths":["测试/目录/文件.txt"],"recursive":false}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(resourceTypeHeader, "instance")
	request.Header.Set(resourceIDHeader, "files-test")

	recorder := httptest.NewRecorder()
	server.handleFiles(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, "测试", "目录", "文件.txt")); !os.IsNotExist(err) {
		t.Fatalf("file should have been deleted, stat err = %v", err)
	}
}
