package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProxyRelaysSessionCookieWithoutExposingIt(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/auth/login":
			http.SetCookie(writer, &http.Cookie{Name: "prism_session", Value: "remote-secret", Path: "/", HttpOnly: true, Secure: true})
			_, _ = writer.Write([]byte(`{"success":true,"data":{}}`))
		case "/api/v1/auth/session":
			if request.Header.Get("Cookie") != "prism_session=remote-secret" {
				http.Error(writer, "missing upstream session", http.StatusUnauthorized)
				return
			}
			_, _ = writer.Write([]byte(`{"success":true,"data":{"authenticated":true}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer remote.Close()

	target, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := New(Config{Target: target, AllowedOrigins: []string{"wails://wails.localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Close(context.Background()) }()
	sessionID, err := proxy.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{}

	loginRequest, err := http.NewRequest(http.MethodPost, proxy.URL()+"/api/v1/auth/login", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	loginRequest.Header.Set(ClientSessionHeader, sessionID)
	loginRequest.Header.Set("Origin", "wails://wails.localhost")
	loginResponse, err := client.Do(loginRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer loginResponse.Body.Close()
	if loginResponse.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", loginResponse.StatusCode)
	}
	if cookies := loginResponse.Cookies(); len(cookies) != 0 {
		t.Fatalf("proxy exposed upstream cookies: %#v", cookies)
	}

	sessionRequest, err := http.NewRequest(http.MethodGet, proxy.URL()+"/api/v1/auth/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionRequest.Header.Set(ClientSessionHeader, sessionID)
	sessionResponse, err := client.Do(sessionRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer sessionResponse.Body.Close()
	if sessionResponse.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d", sessionResponse.StatusCode)
	}
}

func TestProxyRejectsUnknownOrigin(t *testing.T) {
	target, err := url.Parse("https://panel.example.test")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := New(Config{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := proxy.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	request.Header.Set("Origin", "https://untrusted.example")
	request.Header.Set(ClientSessionHeader, sessionID)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestProxyAllowsChunkedUploadHeaders(t *testing.T) {
	target, err := url.Parse("https://panel.example.test")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := New(Config{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/files/proxy/upload", nil)
	request.Header.Set("Origin", "wails://wails.localhost")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", strings.Join([]string{
		"authorization", "content-type", "x-prism-client-session", "x-prism-resource-type",
		"x-prism-resource-id", "x-prism-path", "x-prism-overwrite", "x-prism-upload-id",
		"x-prism-upload-offset", "x-prism-upload-final",
	}, ", "))
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", response.Code, http.StatusNoContent)
	}
	allowedHeaders := strings.ToLower(response.Header().Get("Access-Control-Allow-Headers"))
	for _, header := range []string{"x-prism-upload-id", "x-prism-upload-offset", "x-prism-upload-final"} {
		if !strings.Contains(allowedHeaders, header) {
			t.Fatalf("preflight headers do not allow %q: %q", header, allowedHeaders)
		}
	}
}

func TestProxySessionMustBeCreatedBeforeUse(t *testing.T) {
	target, err := url.Parse("https://panel.example.test")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := New(Config{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	request.Header.Set(ClientSessionHeader, fmt.Sprintf("missing-%d", time.Now().UnixNano()))
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
