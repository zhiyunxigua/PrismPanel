package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ClientSessionHeader = "X-Prism-Client-Session"
	ClientModeHeader    = "X-Prism-Client-Mode"
	ClientModeWinApp    = "winapp"
	proxySessionQuery   = "proxy_session"
)

type Config struct {
	Target         *url.URL
	ListenAddr     string
	AllowedOrigins []string
}

type Server struct {
	config   Config
	target   *url.URL
	sessions *sessionStore
	proxy    *httputil.ReverseProxy

	mu       sync.Mutex
	listener net.Listener
	http     *http.Server
}

type sessionContextKey struct{}

type sessionStore struct {
	mu    sync.Mutex
	items map[string]*session
}

type session struct {
	cookies  map[string]*http.Cookie
	lastSeen time.Time
}

func New(config Config) (*Server, error) {
	if config.Target == nil || config.Target.Host == "" {
		return nil, errors.New("proxy target is required")
	}
	if config.Target.Scheme != "http" && config.Target.Scheme != "https" {
		return nil, errors.New("proxy target must use http or https")
	}
	if config.ListenAddr == "" {
		config.ListenAddr = "127.0.0.1:0"
	}
	if len(config.AllowedOrigins) == 0 {
		config.AllowedOrigins = []string{
			"wails://wails.localhost",
			"http://wails.localhost",
			"https://wails.localhost",
		}
	}
	server := &Server{
		config:   config,
		target:   config.Target,
		sessions: &sessionStore{items: make(map[string]*session)},
	}
	server.proxy = httputil.NewSingleHostReverseProxy(config.Target)
	server.proxy.Director = server.director
	server.proxy.ModifyResponse = server.modifyResponse
	server.proxy.ErrorHandler = server.proxyError
	return server, nil
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.http != nil {
		return errors.New("proxy is already running")
	}
	listener, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen for local proxy: %w", err)
	}
	s.listener = listener
	s.http = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       75 * time.Second,
	}
	go func() {
		_ = s.http.Serve(listener)
	}()
	return nil
}

func (s *Server) Close(ctx context.Context) error {
	s.mu.Lock()
	httpServer := s.http
	s.http = nil
	s.listener = nil
	s.mu.Unlock()
	if httpServer == nil {
		return nil
	}
	return httpServer.Shutdown(ctx)
}

func (s *Server) URL() string {
	s.mu.Lock()
	listener := s.listener
	s.mu.Unlock()
	if listener == nil {
		return ""
	}
	return "http://" + listener.Addr().String()
}

func (s *Server) NewSession() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate proxy session: %w", err)
	}
	id := hex.EncodeToString(raw)
	s.sessions.mu.Lock()
	s.sessions.items[id] = &session{cookies: make(map[string]*http.Cookie), lastSeen: time.Now()}
	s.sessions.mu.Unlock()
	return id, nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	origin := request.Header.Get("Origin")
	if origin != "" && !s.allowedOrigin(origin) {
		writeProxyError(writer, http.StatusForbidden, "FORBIDDEN", "本地代理拒绝了请求来源")
		return
	}
	s.setCORSHeaders(writer, origin)
	if request.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	sessionID := request.Header.Get(ClientSessionHeader)
	if sessionID == "" {
		sessionID = request.URL.Query().Get(proxySessionQuery)
	}
	if !s.sessions.touch(sessionID) {
		writeProxyError(writer, http.StatusUnauthorized, "UNAUTHENTICATED", "本地代理会话无效或已过期")
		return
	}
	s.proxy.ServeHTTP(writer, request)
}

func (s *Server) director(request *http.Request) {
	sessionID := request.Header.Get(ClientSessionHeader)
	if sessionID == "" {
		sessionID = request.URL.Query().Get(proxySessionQuery)
	}
	*request = *request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, sessionID))

	request.Header.Del(ClientSessionHeader)
	request.Header.Set(ClientModeHeader, ClientModeWinApp)
	request.Header.Set("Origin", s.target.Scheme+"://"+s.target.Host)
	query := request.URL.Query()
	query.Del(proxySessionQuery)
	request.URL.RawQuery = query.Encode()

	if cookieHeader := s.sessions.cookieHeader(sessionID); cookieHeader != "" {
		request.Header.Set("Cookie", cookieHeader)
	} else {
		request.Header.Del("Cookie")
	}
	request.Host = s.target.Host
	request.URL.Scheme = s.target.Scheme
	request.URL.Host = s.target.Host
}

func (s *Server) modifyResponse(response *http.Response) error {
	sessionID, _ := response.Request.Context().Value(sessionContextKey{}).(string)
	if sessionID != "" {
		s.sessions.saveCookies(sessionID, response.Cookies())
	}
	response.Header.Del("Set-Cookie")
	return nil
}

func (s *Server) proxyError(writer http.ResponseWriter, _ *http.Request, err error) {
	writeProxyError(writer, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", "远程面板连接失败")
}

func (s *Server) allowedOrigin(origin string) bool {
	for _, allowed := range s.config.AllowedOrigins {
		if strings.EqualFold(strings.TrimRight(allowed, "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}

func (s *Server) setCORSHeaders(writer http.ResponseWriter, origin string) {
	if origin == "" {
		return
	}
	writer.Header().Set("Access-Control-Allow-Origin", origin)
	writer.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Prism-Client-Session, X-Prism-Resource-Type, X-Prism-Resource-ID, X-Prism-Path, X-Prism-Overwrite")
	writer.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, Content-Length, X-Request-ID")
	writer.Header().Add("Vary", "Origin")
}

func writeProxyError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"success": false,
		"error":   map[string]string{"code": code, "message": message},
	})
}

func (s *sessionStore) touch(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[id]
	if item == nil {
		return false
	}
	item.lastSeen = time.Now()
	return true
}

func (s *sessionStore) cookieHeader(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[id]
	if item == nil {
		return ""
	}
	names := make([]string, 0, len(item.cookies))
	for name := range item.cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		cookie := item.cookies[name]
		if cookie != nil {
			parts = append(parts, cookie.Name+"="+cookie.Value)
		}
	}
	return strings.Join(parts, "; ")
}

func (s *sessionStore) saveCookies(id string, cookies []*http.Cookie) {
	if id == "" || len(cookies) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.items[id]
	if item == nil {
		return
	}
	item.lastSeen = time.Now()
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		if cookie.MaxAge < 0 {
			delete(item.cookies, cookie.Name)
			continue
		}
		copy := *cookie
		item.cookies[cookie.Name] = &copy
	}
}
