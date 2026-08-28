package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"PrismPanel/internal/store"
)

type fileProxyGrant struct {
	DaemonTicket string
	NodeID       string
	Scope        string
	UserID       string
	ExpiresAt    time.Time
	MaxUses      int
	uses         int
}

type fileProxyStore struct {
	mu     sync.Mutex
	grants map[string]*fileProxyGrant
}

func newFileProxyStore() *fileProxyStore {
	return &fileProxyStore{grants: make(map[string]*fileProxyGrant)}
}

func (s *fileProxyStore) Add(grant fileProxyGrant) (string, error) {
	if grant.MaxUses <= 0 {
		grant.MaxUses = 1
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	s.mu.Lock()
	for key, item := range s.grants {
		if time.Now().After(item.ExpiresAt) {
			delete(s.grants, key)
		}
	}
	s.grants[token] = &grant
	s.mu.Unlock()
	return token, nil
}

func (s *fileProxyStore) Consume(token, userID, nodeID, scope string) (fileProxyGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	grant := s.grants[token]
	if grant == nil || grant.uses >= grant.MaxUses || time.Now().After(grant.ExpiresAt) {
		delete(s.grants, token)
		return fileProxyGrant{}, apiError("UNAUTHENTICATED", "文件代理凭证无效或已过期")
	}
	if grant.UserID != userID || grant.NodeID != nodeID || grant.Scope != scope {
		return fileProxyGrant{}, apiError("FORBIDDEN", "文件代理凭证不允许当前操作")
	}
	grant.uses++
	if grant.uses >= grant.MaxUses {
		delete(s.grants, token)
	}
	return *grant, nil
}

var fileScopePermissions = map[string]string{
	"file.list": "file.read", "file.read": "file.read", "file.download": "file.read",
	"file.edit": "file.write", "file.upload": "file.write", "file.create": "file.write",
	"file.import": "file.write",
	"file.move":   "file.write", "file.copy": "file.write", "file.archive": "file.write", "file.delete": "file.delete",
}

var fileScopeOperations = map[string]string{
	"file.list": "list", "file.read": "content", "file.download": "download",
	"file.edit": "content", "file.upload": "upload", "file.create": "create",
	"file.import": "import",
	"file.move":   "move", "file.copy": "copy", "file.archive": "archive", "file.delete": "delete",
}

// fileScopeMethods 声明每个文件操作 scope 对应的 HTTP 方法，必须与 daemon 侧
// createTicket 的 allowed 映射保持一致。ticket.create 时显式携带 method，
// 使票据绑定的方法与前端实际发出的请求方法一致（file.edit 为 PUT），
// 避免依赖 daemon 侧从 scope 推导 method 而出现版本不一致导致的
// 「临时凭证不允许当前文件操作」（ConsumeRestrictedFrom method 不匹配）。
var fileScopeMethods = map[string]string{
	"file.list": http.MethodPost, "file.read": http.MethodGet,
	"file.edit": http.MethodPut, "file.upload": http.MethodPost,
	"file.import":   http.MethodPost,
	"file.download": http.MethodGet, "file.create": http.MethodPost,
	"file.move": http.MethodPost, "file.copy": http.MethodPost,
	"file.archive": http.MethodPost, "file.delete": http.MethodPost,
}

type fileAuthorizeInput struct {
	NodeID          string   `json:"node_id"`
	Scope           string   `json:"scope"`
	ResourceType    string   `json:"resource_type"`
	ResourceID      string   `json:"resource_id"`
	Path            string   `json:"path"`
	Paths           []string `json:"paths,omitempty"`
	Size            int64    `json:"size,omitempty"`
	SHA256          string   `json:"sha256,omitempty"`
	ExpectedVersion string   `json:"expected_version,omitempty"`
	ForceProxy      bool     `json:"force_proxy,omitempty"`
	Overwrite       bool     `json:"overwrite,omitempty"`
	Recursive       bool     `json:"recursive,omitempty"`
	Chunked         bool     `json:"chunked,omitempty"`
}

func (s *Server) handleFileAuthorize(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input fileAuthorizeInput
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	input.NodeID = strings.TrimSpace(input.NodeID)
	input.Scope = strings.TrimSpace(input.Scope)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.Path = strings.TrimSpace(input.Path)
	permission, knownScope := fileScopePermissions[input.Scope]
	if err == nil && !knownScope {
		err = apiError("INVALID_REQUEST", "不支持的文件操作范围")
	}
	if err == nil {
		err = s.authorize(request, permission)
	}
	if err == nil {
		err = validateFileAuthorization(input.NodeID, input.ResourceType, input.ResourceID, input.Path, input.Paths)
	}
	node, nodeErr := s.nodes.Get(request.Context(), input.NodeID)
	if err == nil {
		err = nodeErr
	}
	if err == nil && !containsString(node.Capabilities, "files") {
		err = apiError("UNSUPPORTED_CAPABILITY", "目标节点尚不支持文件管理")
	}
	publicURL := strings.TrimRight(node.PublicURL, "/")
	if publicURL == "" {
		publicURL = strings.TrimRight(node.ReportedPublicURL, "/")
	}
	operationName := fileScopeOperations[input.Scope]
	mustProxy := input.Scope == "file.create" || input.Scope == "file.move" || input.Scope == "file.copy" || input.Scope == "file.archive" ||
		input.Scope == "file.delete" || input.Scope == "file.import"
	directSourceIP, directSourceKnown := s.directAccessSourceIP(request)
	direct := err == nil && !input.ForceProxy && publicURL != "" && !mustProxy && directSourceKnown

	mutating := input.Scope == "file.edit" || input.Scope == "file.upload" || input.Scope == "file.import" ||
		input.Scope == "file.create" || input.Scope == "file.move" || input.Scope == "file.copy" || input.Scope == "file.archive" || input.Scope == "file.delete"
	var operation store.FileOperation
	if err == nil && mutating {
		expiresAt := time.Now().UTC().Add(2 * time.Minute)
		if input.Chunked && input.Scope == "file.upload" {
			expiresAt = time.Now().UTC().Add(15 * time.Minute)
		}
		operation, err = s.store.CreateFileOperation(request.Context(), newFileOperation(request, input, expiresAt))
	}

	var issued struct {
		TicketID  string    `json:"ticket_id"`
		Ticket    string    `json:"ticket"`
		Expires   time.Time `json:"expires_at"`
		MaxBytes  int64     `json:"max_bytes"`
		MaxUses   int       `json:"max_uses"`
		ChunkSize int64     `json:"chunk_size"`
	}
	if err == nil {
		ttlSeconds := 120
		if input.Chunked && input.Scope == "file.upload" {
			ttlSeconds = 900
		}
		ticketInput := map[string]any{
			"scope": input.Scope, "resource_type": input.ResourceType, "resource_id": input.ResourceID,
			"path": input.Path, "paths": input.Paths, "size": input.Size, "sha256": input.SHA256,
			"expected_version": input.ExpectedVersion,
			"method":           fileScopeMethods[input.Scope],
			"operation_id":     operation.ID, "ttl_seconds": ttlSeconds,
			"overwrite": input.Overwrite, "recursive": input.Recursive,
			"chunked": input.Chunked,
		}
		if direct {
			ticketInput["source_ip"] = directSourceIP
			ticketInput["session_id"] = hex.EncodeToString(currentSession(request).TokenHash)
		}
		issueTicket := func() error {
			callContext, cancel := context.WithTimeout(request.Context(), 10*time.Second)
			defer cancel()
			return s.connections.Call(callContext, input.NodeID, "ticket.create", ticketInput, &issued)
		}
		err = issueTicket()
		if err != nil && direct {
			delete(ticketInput, "source_ip")
			delete(ticketInput, "session_id")
			direct = false
			err = issueTicket()
		}
	}
	detail := map[string]any{
		"node_id": input.NodeID, "resource_type": input.ResourceType,
		"resource_id": input.ResourceID, "path": input.Path, "paths": input.Paths,
		"size": input.Size,
	}
	if err != nil {
		if operation.ID != "" {
			_, _, _ = s.store.CompleteFileOperation(request.Context(), operation.ID, input.NodeID, false, errorCode(err))
		}
		s.record(request, input.Scope, operation.ID, detail, err)
		writeRequestError(writer, err)
		return
	}
	if !mutating {
		s.record(request, input.Scope+".authorize", "", detail, nil)
	}
	mode := "proxy"
	endpoint := "/api/v1/files/proxy/" + operationName + "?node_id=" + url.QueryEscape(input.NodeID)
	responseTicket := ""
	if direct {
		mode = "direct"
		endpoint = publicURL + "/api/v1/files/" + operationName
		responseTicket = issued.Ticket
	} else {
		responseTicket, err = s.fileProxies.Add(fileProxyGrant{
			DaemonTicket: issued.Ticket, NodeID: input.NodeID, Scope: input.Scope,
			UserID: currentSession(request).User.ID, ExpiresAt: issued.Expires, MaxUses: issued.MaxUses,
		})
		if err != nil {
			writeRequestError(writer, err)
			return
		}
	}
	writeSuccess(writer, map[string]any{
		"operation_id": operation.ID, "mode": mode, "endpoint": endpoint,
		"ticket_id": issued.TicketID, "ticket": responseTicket,
		"expires_at": issued.Expires, "max_bytes": issued.MaxBytes,
		"max_uses": issued.MaxUses, "chunk_size": issued.ChunkSize,
		"resource_type": input.ResourceType, "resource_id": input.ResourceID,
		"path": input.Path,
	})
}

func (s *Server) handleFileExport(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	input := fileAuthorizeInput{
		NodeID:       strings.TrimSpace(request.URL.Query().Get("node_id")),
		Scope:        "file.download",
		ResourceType: strings.TrimSpace(request.URL.Query().Get("resource_type")),
		ResourceID:   strings.TrimSpace(request.URL.Query().Get("resource_id")),
		Path:         strings.TrimSpace(request.URL.Query().Get("path")),
	}
	err := s.authorize(request, "file.read")
	if err == nil {
		err = validateFileAuthorization(input.NodeID, input.ResourceType, input.ResourceID, input.Path, nil)
	}
	node, nodeErr := s.nodes.Get(request.Context(), input.NodeID)
	if err == nil {
		err = nodeErr
	}
	if err == nil && !containsString(node.Capabilities, "files") {
		err = apiError("UNSUPPORTED_CAPABILITY", "目标节点尚不支持文件管理")
	}
	if err != nil {
		writeRequestError(writer, err)
		return
	}

	var issued struct {
		Ticket string
	}
	callContext, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	err = s.connections.Call(callContext, input.NodeID, "ticket.create", map[string]any{
		"scope": input.Scope, "resource_type": input.ResourceType, "resource_id": input.ResourceID,
		"path": input.Path, "method": http.MethodGet, "ttl_seconds": 120,
	}, &issued)
	if err != nil {
		s.record(request, "file.download", input.ResourceID, map[string]any{"path": input.Path}, err)
		writeRequestError(writer, err)
		return
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+issued.Ticket)
	headers.Set("X-Prism-Resource-Type", input.ResourceType)
	headers.Set("X-Prism-Resource-ID", input.ResourceID)
	if value := request.Header.Get("Range"); value != "" {
		headers.Set("Range", value)
	}
	query := make(url.Values)
	query.Set("path", input.Path)
	response, err := s.connections.FileRequest(
		request.Context(), input.NodeID, "download", http.MethodGet, headers, query, nil, 0,
	)
	if err != nil {
		s.record(request, "file.download", input.ResourceID, map[string]any{"path": input.Path}, err)
		writeRequestError(writer, err)
		return
	}
	defer response.Body.Close()
	copyFileResponseHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	_, copyErr := io.Copy(writer, response.Body)
	s.record(request, "file.download", input.ResourceID, map[string]any{"path": input.Path}, copyErr)
}

func copyFileResponseHeaders(destination, source http.Header) {
	for _, name := range []string{
		"Content-Type", "Content-Length", "Content-Disposition", "Accept-Ranges", "Content-Range",
		"Last-Modified", "ETag",
	} {
		if value := source.Get(name); value != "" {
			destination.Set(name, value)
		}
	}
}

func (s *Server) handleFileProxy(writer http.ResponseWriter, request *http.Request) {
	operation := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/files/proxy/"), "/")
	scope := proxyFileScope(operation, request.Method)
	permission := fileScopePermissions[scope]
	if permission == "" {
		http.NotFound(writer, request)
		return
	}
	if err := s.authorize(request, permission); err != nil {
		writeRequestError(writer, err)
		return
	}
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	if nodeID == "" {
		writeRequestError(writer, apiError("INVALID_REQUEST", "必须指定目标节点"))
		return
	}
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		writeRequestError(writer, apiError("UNAUTHENTICATED", "缺少文件代理凭证"))
		return
	}
	grant, err := s.fileProxies.Consume(
		strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")),
		currentSession(request).User.ID, nodeID, scope,
	)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	headers := make(http.Header)
	for _, name := range []string{
		"Content-Type", "Range", "X-Prism-Resource-Type",
		"X-Prism-Resource-ID", "X-Prism-Overwrite", "X-Prism-Expected-Version",
		"X-Prism-Upload-Offset", "X-Prism-Upload-Final",
	} {
		if value := request.Header.Get(name); value != "" {
			headers.Set(name, value)
		}
	}
	headers.Set("Authorization", "Bearer "+grant.DaemonTicket)
	// 路径经 URL query 透传（百分号编码，支持中文等非 Latin-1 字符）；
	// header 中的 X-Prism-Path 不复制，避免 Go 写请求时对非 ASCII 头值报错。
	// 兼容旧客户端：query 缺失时回退读取 X-Prism-Path header（仅 ASCII 路径可用）。
	query := make(url.Values)
	path := strings.TrimSpace(request.URL.Query().Get("path"))
	if path == "" {
		path = strings.TrimSpace(request.Header.Get("X-Prism-Path"))
	}
	if path != "" {
		query.Set("path", path)
	}
	response, err := s.connections.FileRequest(
		request.Context(), nodeID, operation, request.Method, headers, query, request.Body, request.ContentLength,
	)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	defer response.Body.Close()
	copyFileResponseHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

func proxyFileScope(operation, method string) string {
	switch operation {
	case "list":
		if method == http.MethodPost {
			return "file.list"
		}
	case "content":
		if method == http.MethodGet {
			return "file.read"
		}
		if method == http.MethodPut {
			return "file.edit"
		}
	case "upload":
		if method == http.MethodPost {
			return "file.upload"
		}
	case "import":
		if method == http.MethodPost {
			return "file.import"
		}
	case "download":
		if method == http.MethodGet {
			return "file.download"
		}
	case "create":
		if method == http.MethodPost {
			return "file.create"
		}
	case "move":
		if method == http.MethodPost {
			return "file.move"
		}
	case "copy":
		if method == http.MethodPost {
			return "file.copy"
		}
	case "archive":
		if method == http.MethodPost {
			return "file.archive"
		}
	case "delete":
		if method == http.MethodPost {
			return "file.delete"
		}
	}
	return ""
}

func validateFileAuthorization(nodeID, resourceType, resourceID, path string, paths []string) error {
	if nodeID == "" || resourceID == "" || (resourceType != "instance" && resourceType != "image") {
		return apiError("INVALID_REQUEST", "文件操作缺少有效的节点或资源")
	}
	if path == "" || len(path) > 4096 || strings.ContainsRune(path, 0) || len(paths) > 100 {
		return apiError("INVALID_REQUEST", "文件路径无效")
	}
	for _, candidate := range paths {
		if candidate == "" || len(candidate) > 4096 || strings.ContainsRune(candidate, 0) {
			return apiError("INVALID_REQUEST", "文件路径无效")
		}
	}
	return nil
}

func newFileOperation(request *http.Request, input fileAuthorizeInput, expiresAt time.Time) store.FileOperation {
	session := currentSession(request)
	sessionID := ""
	if len(session.TokenHash) > 0 {
		sessionID = hex.EncodeToString(session.TokenHash)
	}
	return store.FileOperation{
		RequestID: requestID(request), ExpiresAt: expiresAt, ActorUserID: session.User.ID,
		SessionID: sessionID, ActorUsername: session.User.Username,
		ActorDisplayName: session.User.DisplayName, SourceIP: clientIP(request),
		UserAgent: request.UserAgent(), Action: input.Scope, NodeID: input.NodeID,
		ResourceType: input.ResourceType, ResourceID: input.ResourceID,
		Detail: map[string]any{"path": input.Path, "paths": input.Paths, "size": input.Size},
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
