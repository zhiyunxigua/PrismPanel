package api

// Regression tests for the "file.edit ticket validation" bug reported from the
// panel web FileManager ("临时凭证不允许当前文件操作" on save).
//
// The panel's /api/v1/files/authorize now sends the HTTP method explicitly in
// ticket.create (file.edit -> PUT). These tests lock the contract:
//   - a panel-style ticket (no method field) is still derived correctly (back-compat),
//   - an explicit-method ticket matches the request method,
//   - the path carried in the URL query (percent-encoded UTF-8, Chinese paths)
//     matches the ticket path recorded from the JSON body,
//   - method/path mismatches are rejected with PERMISSION_DENIED.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// createFileTicketPanelStyle mimics panel/internal/api/file_handlers.go
// handleFileAuthorize: it omits "method" and passes "paths": [].
func createFileTicketPanelStyle(t *testing.T, server *Server, scope, path string) string {
	t.Helper()
	return createFileTicketWithMethod(t, server, scope, path, "")
}

// createFileTicketWithMethod mimics the fixed panel authorize: it passes the
// explicit method (or empty for the legacy path) plus "paths": [].
func createFileTicketWithMethod(t *testing.T, server *Server, scope, path, method string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"scope": scope, "resource_type": "instance", "resource_id": "files-test",
		"path": path, "paths": []string{}, "method": method, "ttl_seconds": 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := server.createTicket(raw)
	if err != nil {
		t.Fatalf("create %s ticket (method=%q): %v", scope, method, err)
	}
	token, ok := issued.(map[string]any)["ticket"].(string)
	if !ok || token == "" {
		t.Fatalf("ticket response missing token: %#v", issued)
	}
	return token
}

// readVersion 模拟前端流程：先用 file.read 打开文件拿到 version，供保存时携带。
func readVersion(t *testing.T, server *Server, path string) string {
	t.Helper()
	token := createFileTicketWithMethod(t, server, "file.read", path, http.MethodGet)
	target := "http://daemon/api/v1/files/content?path=" + url.QueryEscape(path)
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(resourceTypeHeader, "instance")
	request.Header.Set(resourceIDHeader, "files-test")
	recorder := httptest.NewRecorder()
	server.handleFiles(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("read status = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Data.Version
}

func putContent(t *testing.T, server *Server, token, path, content, version string) *httptest.ResponseRecorder {
	t.Helper()
	target := "http://daemon/api/v1/files/content?path=" + url.QueryEscape(path)
	body := `{"content":"` + content + `","encoding":"utf-8","expected_version":"` + version + `"}`
	request := httptest.NewRequest(http.MethodPut, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set(resourceTypeHeader, "instance")
	request.Header.Set(resourceIDHeader, "files-test")
	recorder := httptest.NewRecorder()
	server.handleFiles(recorder, request)
	return recorder
}

func TestFileEditPutPanelStyleTicket(t *testing.T) {
	// 旧面板/旧客户端：ticket.create 不带 method，daemon 从 scope 推导 PUT。
	server, workspace := newFilesTestServer(t)
	writeWorkspaceFile(t, workspace, "config/plugins.txt", "old content")
	version := readVersion(t, server, "config/plugins.txt")

	token := createFileTicketPanelStyle(t, server, "file.edit", "config/plugins.txt")
	recorder := putContent(t, server, token, "config/plugins.txt", "new content", version)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "new content") {
		t.Fatalf("unexpected save result: %s", recorder.Body.String())
	}
}

func TestFileEditPutExplicitMethodTicket(t *testing.T) {
	// 修复后的面板：ticket.create 显式携带 method=PUT。
	server, workspace := newFilesTestServer(t)
	writeWorkspaceFile(t, workspace, "config/plugins.txt", "old content")
	version := readVersion(t, server, "config/plugins.txt")

	token := createFileTicketWithMethod(t, server, "file.edit", "config/plugins.txt", http.MethodPut)
	recorder := putContent(t, server, token, "config/plugins.txt", "new content", version)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "new content") {
		t.Fatalf("unexpected save result: %s", recorder.Body.String())
	}
}

func TestFileEditPutChinesePathExplicitMethodTicket(t *testing.T) {
	// 中文路径：ticket 创建时 path 经 JSON body，保存时 path 经 URL query
	// （百分号编码 UTF-8），两处规范化后必须一致。
	server, workspace := newFilesTestServer(t)
	writeWorkspaceFile(t, workspace, "测试/配置.txt", "旧内容")
	version := readVersion(t, server, "测试/配置.txt")

	token := createFileTicketWithMethod(t, server, "file.edit", "测试/配置.txt", http.MethodPut)
	recorder := putContent(t, server, token, "测试/配置.txt", "新内容", version)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "新内容") {
		t.Fatalf("unexpected save result: %s", recorder.Body.String())
	}
}

func TestFileEditRejectsMethodMismatch(t *testing.T) {
	// 修复后：面板显式传入 method，与 scope 绑定的方法（file.edit → PUT）不一致时，
	// 必须在票据创建阶段被拒绝（INVALID_TICKET），
	// 而不是创建出方法不匹配的票据、到保存时才报「临时凭证不允许当前文件操作」。
	server, workspace := newFilesTestServer(t)
	writeWorkspaceFile(t, workspace, "config/plugins.txt", "old content")

	raw, err := json.Marshal(map[string]any{
		"scope": "file.edit", "resource_type": "instance", "resource_id": "files-test",
		"path": "config/plugins.txt", "paths": []string{}, "method": http.MethodGet, "ttl_seconds": 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.createTicket(raw); err == nil {
		t.Fatal("expected ticket creation to be rejected for method/scope mismatch")
	} else if !strings.Contains(err.Error(), "文件凭证范围或请求方法无效") {
		t.Fatalf("unexpected create error: %v", err)
	}
}

func TestFileEditRejectsPathMismatch(t *testing.T) {
	// 票据绑定路径 A，却保存到路径 B → 必须 PERMISSION_DENIED。
	server, workspace := newFilesTestServer(t)
	writeWorkspaceFile(t, workspace, "config/a.txt", "old a")
	writeWorkspaceFile(t, workspace, "config/b.txt", "old b")
	versionA := readVersion(t, server, "config/a.txt")

	token := createFileTicketWithMethod(t, server, "file.edit", "config/a.txt", http.MethodPut)
	recorder := putContent(t, server, token, "config/b.txt", "new b", versionA)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body: %s)", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "临时凭证不允许当前文件操作") {
		t.Fatalf("unexpected error body: %s", recorder.Body.String())
	}
}
