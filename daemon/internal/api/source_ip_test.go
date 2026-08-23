package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"PrismPanel-daemon/internal/config"
)

func TestRequestSourceIPUsesDirectPeer(t *testing.T) {
	server := &Server{config: config.Default()}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.4:42000"

	if source := server.requestSourceIP(request); source != "198.51.100.4" {
		t.Fatalf("source = %q", source)
	}
}

func TestRequestSourceIPUsesTrustedProxyChain(t *testing.T) {
	cfg := config.Default()
	cfg.Security.TrustedProxyCIDRs = []string{"127.0.0.1/32", "192.168.0.0/16"}
	server := &Server{config: cfg}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("X-Forwarded-For", "203.0.113.20, 192.168.1.10")

	if source := server.requestSourceIP(request); source != "203.0.113.20" {
		t.Fatalf("source = %q", source)
	}
}

func TestRequestSourceIPRejectsUntrustedForwarding(t *testing.T) {
	server := &Server{config: config.Default()}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.4:42000"
	request.Header.Set("X-Forwarded-For", "203.0.113.20")

	if source := server.requestSourceIP(request); source != "" {
		t.Fatalf("source = %q, want empty", source)
	}
}
