package api

// Consistency tests for the file ticket method binding introduced with the
// "临时凭证不允许当前文件操作" (file.edit ticket validation) fix:
// the panel now passes the explicit HTTP method in ticket.create, so the
// method used for the proxy request must agree with the ticket scope.

import (
	"net/http"
	"testing"
)

func TestFileScopeMethodsConsistentWithProxyMapping(t *testing.T) {
	// 每个 scope 声明的 HTTP 方法必须与 proxyFileScope(operation, method)
	// 双向一致：授权时按此方法创建票据，代理转发时按此方法消费票据。
	for scope, method := range fileScopeMethods {
		operation, ok := fileScopeOperations[scope]
		if !ok {
			t.Fatalf("scope %s missing from fileScopeOperations", scope)
		}
		if got := proxyFileScope(operation, method); got != scope {
			t.Fatalf("proxyFileScope(%q, %q) = %q, want %q", operation, method, got, scope)
		}
		if _, ok := fileScopePermissions[scope]; !ok {
			t.Fatalf("scope %s missing from fileScopePermissions", scope)
		}
	}
	for scope := range fileScopePermissions {
		if _, ok := fileScopeMethods[scope]; !ok {
			t.Fatalf("scope %s missing from fileScopeMethods", scope)
		}
	}
}

func TestFileScopeMethodsMatchDaemonContract(t *testing.T) {
	// 与 daemon createTicket 的 allowed 映射保持一致：
	// file.edit 必须为 PUT，file.read/file.download 必须为 GET，其余写操作为 POST。
	expected := map[string]string{
		"file.list":     http.MethodPost,
		"file.read":     http.MethodGet,
		"file.edit":     http.MethodPut,
		"file.upload":   http.MethodPost,
		"file.import":   http.MethodPost,
		"file.download": http.MethodGet,
		"file.create":   http.MethodPost,
		"file.move":     http.MethodPost,
		"file.copy":     http.MethodPost,
		"file.archive":  http.MethodPost,
		"file.delete":   http.MethodPost,
	}
	for scope, method := range expected {
		if got := fileScopeMethods[scope]; got != method {
			t.Fatalf("fileScopeMethods[%q] = %q, want %q", scope, got, method)
		}
	}
}
