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
	used         bool
}

type fileProxyStore struct {
	mu     sync.Mutex
	grants map[string]*fileProxyGrant
}

func newFileProxyStore() *fileProxyStore {
	return &fileProxyStore{grants: make(map[string]*fileProxyGrant)}
}

func (s *fileProxyStore) Add(grant fileProxyGrant) (string, error) {
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
	if grant == nil || grant.used || time.Now().After(grant.ExpiresAt) {
		delete(s.grants, token)
		return fileProxyGrant{}, apiError("UNAUTHENTICATED", "文件代理凭证无效或已过期")
	}
	if grant.UserID != userID || grant.NodeID != nodeID || grant.Scope != scope {
		return fileProxyGrant{}, apiError("FORBIDDEN", "文件代理凭证不允许当前操作")
	}
	grant.used = true
	delete(s.grants, token)
	return *grant, nil
}

var fileScopePermissions = map[string]string{
	"file.list": "file.read", "file.read": "file.read", "file.download": "file.read",
	"file.edit": "file.write", "file.upload": "file.write", "file.create": "file.write",
	"file.import": "file.write",
	"file.move":   "file.write", "file.delete": "file.delete",
}

var fileScopeOperations = map[string]string{
	"file.list": "list", "file.read": "content", "file.download": "download",
	"file.edit": "content", "file.upload": "upload", "file.create": "create",
	"file.import": "import",
	"file.move":   "move", "file.delete": "delete",
}

type fileAuthorizeInput struct {
	NodeID       string   `json:"node_id"`
	Scope        string   `json:"scope"`
	ResourceType string   `json:"resource_type"`
	ResourceID   string   `json:"resource_id"`
	Path         string   `json:"path"`
	Paths        []string `json:"paths,omitempty"`
	Size         int64    `json:"size,omitempty"`
	SHA256       string   `json:"sha256,omitempty"`
	ForceProxy   bool     `json:"force_proxy,omitempty"`
	Overwrite    bool     `json:"overwrite,omitempty"`
	Recursive    bool     `json:"recursive,omitempty"`
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

	mutating := input.Scope == "file.edit" || input.Scope == "file.upload" || input.Scope == "file.import" ||
		input.Scope == "file.create" || input.Scope == "file.move" || input.Scope == "file.delete"
	var operation store.FileOperation
	if err == nil && mutating {
		expiresAt := time.Now().UTC().Add(2 * time.Minute)
		operation, err = s.store.CreateFileOperation(request.Context(), newFileOperation(request, input, expiresAt))
	}

	var issued struct {
		TicketID string    `json:"ticket_id"`
		Ticket   string    `json:"ticket"`
		Expires  time.Time `json:"expires_at"`
		MaxBytes int64     `json:"max_bytes"`
	}
	if err == nil {
		callContext, cancel := context.WithTimeout(request.Context(), 10*time.Second)
		defer cancel()
		err = s.connections.Call(callContext, input.NodeID, "ticket.create", map[string]any{
			"scope": input.Scope, "resource_type": input.ResourceType, "resource_id": input.ResourceID,
			"path": input.Path, "paths": input.Paths, "size": input.Size, "sha256": input.SHA256,
			"operation_id": operation.ID, "ttl_seconds": 120,
			"overwrite": input.Overwrite, "recursive": input.Recursive,
		}, &issued)
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
	publicURL := strings.TrimRight(node.PublicURL, "/")
	if publicURL == "" {
		publicURL = strings.TrimRight(node.ReportedPublicURL, "/")
	}
	operationName := fileScopeOperations[input.Scope]
	mode := "direct"
	endpoint := publicURL + "/api/v1/files/" + operationName
	mustProxy := input.Scope == "file.create" || input.Scope == "file.move" ||
		input.Scope == "file.delete" || input.Scope == "file.import"
	responseTicket := issued.Ticket
	if input.ForceProxy || publicURL == "" || mustProxy {
		mode = "proxy"
		endpoint = "/api/v1/files/proxy/" + operationName + "?node_id=" + url.QueryEscape(input.NodeID)
		responseTicket, err = s.fileProxies.Add(fileProxyGrant{
			DaemonTicket: issued.Ticket, NodeID: input.NodeID, Scope: input.Scope,
			UserID: currentSession(request).User.ID, ExpiresAt: issued.Expires,
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
		"resource_type": input.ResourceType, "resource_id": input.ResourceID,
		"path": input.Path,
	})
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
		"X-Prism-Resource-ID", "X-Prism-Path", "X-Prism-Overwrite",
	} {
		if value := request.Header.Get(name); value != "" {
			headers.Set(name, value)
		}
	}
	headers.Set("Authorization", "Bearer "+grant.DaemonTicket)
	response, err := s.connections.FileRequest(
		request.Context(), nodeID, operation, request.Method, headers, request.Body, request.ContentLength,
	)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	defer response.Body.Close()
	for _, name := range []string{
		"Content-Type", "Content-Length", "Content-Disposition", "Accept-Ranges", "Content-Range",
	} {
		if value := response.Header.Get(name); value != "" {
			writer.Header().Set(name, value)
		}
	}
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
