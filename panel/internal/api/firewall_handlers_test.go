package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"PrismPanel/internal/config"
	"PrismPanel/internal/daemon"
)

func TestDirectAccessSourceIPUsesTrustedProxyChain(t *testing.T) {
	cfg := config.Default()
	cfg.Security.TrustedProxyCIDRs = []string{"10.0.0.0/8", "192.168.0.0/16"}
	server := &Server{config: cfg}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:42000"
	request.Header.Set("X-Forwarded-For", "203.0.113.20, 192.168.1.10")

	source, ok := server.directAccessSourceIP(request)
	if !ok || source != "203.0.113.20" {
		t.Fatalf("source = %q, ok = %v", source, ok)
	}
}

func TestDirectAccessSourceIPRejectsUntrustedForwarding(t *testing.T) {
	server := &Server{config: config.Default()}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.4:42000"
	request.Header.Set("X-Forwarded-For", "203.0.113.20")
	if source, ok := server.directAccessSourceIP(request); ok || source != "" {
		t.Fatalf("untrusted forwarding returned source %q", source)
	}
	request.Header.Del("X-Forwarded-For")
	if source, ok := server.directAccessSourceIP(request); !ok || source != "198.51.100.4" {
		t.Fatalf("direct source = %q, ok = %v", source, ok)
	}
}

func TestHandleClientIPReturnsDirectSource(t *testing.T) {
	server := &Server{config: config.Default()}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/client-ip", nil)
	request.RemoteAddr = "203.0.113.20:42000"
	recorder := httptest.NewRecorder()

	server.handleClientIP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), "203.0.113.20") {
		t.Fatalf("response does not contain source IP: %s", recorder.Body.String())
	}
}

func TestFirewallDaemonErrorHTTPMapping(t *testing.T) {
	tests := map[string]int{
		"FIREWALL_REVISION_CONFLICT": http.StatusConflict,
		"FIREWALL_RULE_NOT_FOUND":    http.StatusNotFound,
		"FIREWALL_UNSUPPORTED":       http.StatusUnprocessableEntity,
		"FIREWALL_APPLY_FAILED":      http.StatusBadGateway,
	}
	for code, expected := range tests {
		t.Run(code, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			writeRequestError(recorder, &daemon.APIError{Code: code, Message: "test"})
			if recorder.Code != expected {
				t.Fatalf("status = %d, want %d", recorder.Code, expected)
			}
		})
	}
}
