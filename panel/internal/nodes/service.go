package nodes

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"PrismPanel/internal/daemon"
	"PrismPanel/internal/store"
)

type Input struct {
	Name      string
	BaseURL   string
	PublicURL string
	Token     string
	Enabled   bool
}

type View struct {
	store.Node
	Status            string `json:"status"`
	LatencyMS         int64  `json:"latency_ms"`
	ReportedPublicURL string `json:"reported_public_url,omitempty"`
	SecurityLevel     string `json:"security_level"`
}

type TestResult struct {
	DaemonID        string   `json:"daemon_id"`
	DaemonVersion   string   `json:"daemon_version"`
	ProtocolVersion string   `json:"protocol_version"`
	Capabilities    []string `json:"capabilities"`
	PublicURL       string   `json:"reported_public_url,omitempty"`
	LatencyMS       int64    `json:"latency_ms"`
}

type Service struct {
	store   *store.Store
	manager *daemon.Manager
	aead    cipher.AEAD
}

func NewService(repository *store.Store, manager *daemon.Manager, masterKey []byte) (*Service, error) {
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("create node token cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Service{store: repository, manager: manager, aead: aead}, nil
}

func (s *Service) LoadConnections(ctx context.Context) ([]daemon.ConnectionDefinition, error) {
	items, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	definitions := make([]daemon.ConnectionDefinition, 0, len(items))
	for _, item := range items {
		token, err := s.decryptToken(item.ID, item.TokenCiphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt token for node %s: %w", item.ID, err)
		}
		definitions = append(definitions, daemon.ConnectionDefinition{
			PanelNodeID: item.ID, BaseURL: item.BaseURL, Token: token, Enabled: item.Enabled,
		})
	}
	return definitions, nil
}

func (s *Service) Test(ctx context.Context, baseURL, token string) (TestResult, error) {
	baseURL, _, err := validateURLs(baseURL, "")
	if err != nil {
		return TestResult{}, err
	}
	if strings.TrimSpace(token) == "" {
		return TestResult{}, errors.New("节点令牌不能为空")
	}
	probeContext, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	metadata, latency, err := daemon.Probe(probeContext, baseURL, token)
	if err != nil {
		return TestResult{}, err
	}
	return TestResult{
		DaemonID: metadata.NodeID, DaemonVersion: metadata.Version,
		ProtocolVersion: metadata.ProtocolVersion, Capabilities: metadata.Capabilities,
		PublicURL: metadata.PublicURL, LatencyMS: latency,
	}, nil
}

func (s *Service) Create(ctx context.Context, input Input) (View, error) {
	input, err := validateInput(input, true)
	if err != nil {
		return View{}, err
	}
	id, err := randomID()
	if err != nil {
		return View{}, err
	}
	ciphertext, err := s.encryptToken(id, input.Token)
	if err != nil {
		return View{}, err
	}
	created, err := s.store.CreateNode(ctx, store.Node{
		ID: id, Name: input.Name, BaseURL: input.BaseURL,
		PublicURL: input.PublicURL, TokenCiphertext: ciphertext, Enabled: input.Enabled,
		Capabilities: []string{},
	})
	if err != nil {
		return View{}, err
	}
	s.manager.Upsert(daemon.ConnectionDefinition{
		PanelNodeID: id, BaseURL: input.BaseURL, Token: input.Token, Enabled: input.Enabled,
	})
	return s.view(created), nil
}

func (s *Service) Update(ctx context.Context, id string, input Input) (View, error) {
	input, err := validateInput(input, false)
	if err != nil {
		return View{}, err
	}
	current, err := s.store.GetNode(ctx, id)
	if err != nil {
		return View{}, err
	}
	token := input.Token
	if token == "" {
		token, err = s.decryptToken(id, current.TokenCiphertext)
		if err != nil {
			return View{}, err
		}
	}
	connectionChanged := current.BaseURL != input.BaseURL || input.Token != ""
	if input.Token != "" {
		current.TokenCiphertext, err = s.encryptToken(id, token)
		if err != nil {
			return View{}, err
		}
	}
	current.Name, current.BaseURL, current.PublicURL, current.Enabled =
		input.Name, input.BaseURL, input.PublicURL, input.Enabled
	if connectionChanged {
		current.DaemonID = ""
		current.DaemonVersion = ""
		current.ProtocolVersion = ""
		current.Capabilities = []string{}
		current.LastConnectedAt = nil
		current.LastError = ""
	}
	updated, err := s.store.UpdateNode(ctx, current)
	if err != nil {
		return View{}, err
	}
	s.manager.Upsert(daemon.ConnectionDefinition{
		PanelNodeID: id, BaseURL: input.BaseURL, Token: token, Enabled: input.Enabled,
	})
	return s.view(updated), nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if _, err := s.store.GetNode(ctx, id); err != nil {
		return err
	}
	if err := s.store.DeleteNode(ctx, id); err != nil {
		return err
	}
	s.manager.Remove(id)
	return nil
}

func (s *Service) Get(ctx context.Context, id string) (View, error) {
	item, err := s.store.GetNode(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(item), nil
}

func (s *Service) List(ctx context.Context) ([]View, error) {
	items, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(items))
	for _, item := range items {
		views = append(views, s.view(item))
	}
	return views, nil
}

func (s *Service) view(node store.Node) View {
	runtime := s.manager.Status(node.ID)
	status := runtime.State
	if !node.Enabled {
		status = "DISABLED"
	} else if status == "ONLINE" && runtime.ProtocolVersion != "" && runtime.ProtocolVersion != "1" {
		status = "DEGRADED"
	}
	if runtime.Version != "" {
		node.DaemonVersion = runtime.Version
		node.ProtocolVersion = runtime.ProtocolVersion
		node.Capabilities = runtime.Capabilities
	}
	if runtime.NodeID != "" {
		node.DaemonID = runtime.NodeID
	}
	if !runtime.ConnectedAt.IsZero() {
		node.LastConnectedAt = &runtime.ConnectedAt
	}
	if runtime.LastError != "" {
		node.LastError = runtime.LastError
	}
	securityLevel := "unencrypted"
	if strings.HasPrefix(node.BaseURL, "https://") {
		securityLevel = "encrypted"
	}
	return View{
		Node: node, Status: status, LatencyMS: runtime.LatencyMS,
		ReportedPublicURL: runtime.PublicURL, SecurityLevel: securityLevel,
	}
}

func validateInput(input Input, requireToken bool) (Input, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Token = strings.TrimSpace(input.Token)
	if input.Name == "" || len([]rune(input.Name)) > 100 {
		return Input{}, errors.New("节点名称不能为空且不能超过 100 个字符")
	}
	if requireToken && input.Token == "" {
		return Input{}, errors.New("节点令牌不能为空")
	}
	baseURL, publicURL, err := validateURLs(input.BaseURL, input.PublicURL)
	if err != nil {
		return Input{}, err
	}
	input.BaseURL, input.PublicURL = baseURL, publicURL
	return input, nil
}

func validateURLs(base, public string) (string, string, error) {
	baseURL, err := normalizeHTTPURL("连接地址", base, false)
	if err != nil {
		return "", "", err
	}
	publicURL, err := normalizeHTTPURL("公网地址", public, true)
	return baseURL, publicURL, err
}

func normalizeHTTPURL(name, raw string, optional bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if optional && raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Opaque != "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%s 必须是完整的 HTTP 或 HTTPS URL", name)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%s 必须是完整的 HTTP 或 HTTPS URL", name)
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("%s 必须包含主机名", name)
	}
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	switch {
	case strings.Contains(hostname, ":") && port != "":
		parsed.Host = net.JoinHostPort(hostname, port)
	case strings.Contains(hostname, ":"):
		parsed.Host = "[" + hostname + "]"
	case port != "":
		parsed.Host = net.JoinHostPort(hostname, port)
	default:
		parsed.Host = hostname
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	normalized := parsed.String()
	if len(normalized) > 512 {
		return "", fmt.Errorf("%s 不能超过 512 个字符", name)
	}
	return normalized, nil
}

func (s *Service) encryptToken(id, token string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(token), []byte(id)), nil
}

func (s *Service) decryptToken(id string, ciphertext []byte) (string, error) {
	if len(ciphertext) < s.aead.NonceSize() {
		return "", errors.New("节点令牌密文无效")
	}
	nonce := ciphertext[:s.aead.NonceSize()]
	plain, err := s.aead.Open(nil, nonce, ciphertext[s.aead.NonceSize():], []byte(id))
	if err != nil {
		return "", errors.New("节点令牌无法使用当前面板主密钥解密")
	}
	return string(plain), nil
}

func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
