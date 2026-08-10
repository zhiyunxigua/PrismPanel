package ticket

import (
	"crypto/rand"
	"encoding/base64"
	"net/netip"
	"path"
	"strings"
	"sync"
	"time"

	"PrismPanel-daemon/internal/apperr"
)

type Ticket struct {
	ID              string    `json:"ticket_id"`
	Token           string    `json:"ticket"`
	Scope           string    `json:"scope"`
	InstanceID      string    `json:"instance_id,omitempty"`
	ExpiresAt       time.Time `json:"expires_at"`
	MaxUses         int       `json:"max_uses"`
	SHA256          string    `json:"sha256,omitempty"`
	ExpectedVersion string    `json:"expected_version,omitempty"`
	MaxBytes        int64     `json:"max_bytes,omitempty"`
	ResourceType    string    `json:"resource_type,omitempty"`
	ResourceID      string    `json:"resource_id,omitempty"`
	Path            string    `json:"path,omitempty"`
	Paths           []string  `json:"paths,omitempty"`
	PathPrefix      bool      `json:"path_prefix,omitempty"`
	Method          string    `json:"method,omitempty"`
	OperationID     string    `json:"operation_id,omitempty"`
	AllowOverwrite  bool      `json:"allow_overwrite,omitempty"`
	AllowRecursive  bool      `json:"allow_recursive,omitempty"`
	ClientIP        string    `json:"-"`
	SessionID       string    `json:"-"`
	uses            int
}

type TicketOptions struct {
	ClientIP  string
	SessionID string
}

type RestrictedOptions struct {
	Scope           string
	ResourceType    string
	ResourceID      string
	Path            string
	Paths           []string
	PathPrefix      bool
	Method          string
	OperationID     string
	AllowOverwrite  bool
	AllowRecursive  bool
	MaxBytes        int64
	SHA256          string
	ExpectedVersion string
	TTL             time.Duration
	MaxUses         int
	ClientIP        string
	SessionID       string
}

func (m *Manager) CreateRestricted(options RestrictedOptions) (Ticket, error) {
	if options.ResourceType != "instance" && options.ResourceType != "image" {
		return Ticket{}, apperr.New("INVALID_TICKET", "文件凭证资源类型无效")
	}
	if strings.TrimSpace(options.ResourceID) == "" || strings.TrimSpace(options.Method) == "" {
		return Ticket{}, apperr.New("INVALID_TICKET", "文件凭证缺少资源或方法")
	}
	cleanPath, err := normalizePath(options.Path)
	if err != nil {
		return Ticket{}, err
	}
	cleanPaths := make([]string, 0, len(options.Paths))
	for _, candidate := range options.Paths {
		cleanCandidate, pathErr := normalizePath(candidate)
		if pathErr != nil {
			return Ticket{}, pathErr
		}
		cleanPaths = append(cleanPaths, cleanCandidate)
	}
	if len(cleanPaths) == 0 {
		cleanPaths = []string{cleanPath}
	}
	item, err := m.CreateWithOptions(options.Scope, options.ResourceID, options.TTL, options.MaxUses, TicketOptions{
		ClientIP: options.ClientIP, SessionID: options.SessionID,
	})
	if err != nil {
		return Ticket{}, err
	}
	m.mu.Lock()
	stored := m.byID[item.ID]
	stored.ResourceType = options.ResourceType
	stored.ResourceID = options.ResourceID
	stored.Path = cleanPath
	stored.Paths = cleanPaths
	stored.PathPrefix = options.PathPrefix
	stored.Method = strings.ToUpper(options.Method)
	stored.OperationID = options.OperationID
	stored.AllowOverwrite = options.AllowOverwrite
	stored.AllowRecursive = options.AllowRecursive
	stored.MaxBytes = options.MaxBytes
	stored.SHA256 = strings.ToLower(options.SHA256)
	stored.ExpectedVersion = strings.ToLower(strings.TrimSpace(options.ExpectedVersion))
	item = *stored
	m.mu.Unlock()
	return item, nil
}

func (m *Manager) CreateUpload(scope, resourceID, sha256 string, maxBytes int64, ttl time.Duration) (Ticket, error) {
	if sha256 == "" || maxBytes <= 0 {
		return Ticket{}, apperr.New("INVALID_TICKET", "upload ticket requires sha256 and size")
	}
	item, err := m.Create(scope, resourceID, ttl, 1)
	if err != nil {
		return Ticket{}, err
	}
	m.mu.Lock()
	stored := m.byID[item.ID]
	if stored != nil {
		stored.SHA256 = sha256
		stored.MaxBytes = maxBytes
		item = *stored
	}
	m.mu.Unlock()
	return item, nil
}

type Manager struct {
	mu      sync.Mutex
	byToken map[string]*Ticket
	byID    map[string]*Ticket
}

func NewManager() *Manager {
	return &Manager{byToken: make(map[string]*Ticket), byID: make(map[string]*Ticket)}
}

func (m *Manager) Create(scope, instanceID string, ttl time.Duration, maxUses int) (Ticket, error) {
	return m.CreateWithOptions(scope, instanceID, ttl, maxUses, TicketOptions{})
}

func (m *Manager) CreateWithOptions(scope, instanceID string, ttl time.Duration, maxUses int, options TicketOptions) (Ticket, error) {
	if ttl <= 0 || ttl > 15*time.Minute {
		return Ticket{}, apperr.New("INVALID_TICKET", "临时凭证有效期必须在 15 分钟以内")
	}
	if maxUses <= 0 {
		maxUses = 1
	}
	clientIP, err := normalizeClientIP(options.ClientIP)
	if err != nil {
		return Ticket{}, err
	}
	id, err := randomValue(16)
	if err != nil {
		return Ticket{}, apperr.Wrap("INTERNAL", "无法生成临时凭证 ID", err)
	}
	token, err := randomValue(32)
	if err != nil {
		return Ticket{}, apperr.Wrap("INTERNAL", "无法生成临时凭证", err)
	}
	item := &Ticket{
		ID: id, Token: token, Scope: scope, InstanceID: instanceID,
		ExpiresAt: time.Now().UTC().Add(ttl), MaxUses: maxUses,
		ClientIP: clientIP, SessionID: strings.TrimSpace(options.SessionID),
	}
	m.mu.Lock()
	m.removeExpiredLocked(time.Now())
	m.byToken[token] = item
	m.byID[id] = item
	m.mu.Unlock()
	return *item, nil
}

func (m *Manager) Consume(token, scope, instanceID string) (Ticket, error) {
	return m.ConsumeFrom(token, scope, instanceID, "")
}

func (m *Manager) ConsumeFrom(token, scope, instanceID, source string) (Ticket, error) {
	clientIP, err := normalizeClientIP(source)
	if err != nil {
		return Ticket{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.activeTicketLocked(token)
	if err != nil {
		return Ticket{}, err
	}
	if item.Scope != scope || item.InstanceID != instanceID {
		return Ticket{}, apperr.New("PERMISSION_DENIED", "临时凭证不允许当前操作")
	}
	if err := item.validateSource(clientIP); err != nil {
		return Ticket{}, err
	}
	return consumeTicketLocked(item)
}

func (m *Manager) ConsumeRestricted(token, scope, resourceType, resourceID, requestPath, method string) (Ticket, error) {
	return m.ConsumeRestrictedFrom(token, scope, resourceType, resourceID, requestPath, method, "")
}

func (m *Manager) ConsumeRestrictedFrom(token, scope, resourceType, resourceID, requestPath, method, source string) (Ticket, error) {
	cleanPath, err := normalizePath(requestPath)
	if err != nil {
		return Ticket{}, err
	}
	clientIP, err := normalizeClientIP(source)
	if err != nil {
		return Ticket{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.activeTicketLocked(token)
	if err != nil {
		return Ticket{}, err
	}
	if item.Scope != scope || item.ResourceType != resourceType || item.ResourceID != resourceID ||
		item.Method != strings.ToUpper(method) || !ticketPathAllowed(*item, cleanPath) {
		return Ticket{}, apperr.New("PERMISSION_DENIED", "临时凭证不允许当前文件操作")
	}
	if err := item.validateSource(clientIP); err != nil {
		return Ticket{}, err
	}
	return consumeTicketLocked(item)
}

func (m *Manager) activeTicketLocked(token string) (*Ticket, error) {
	item := m.byToken[token]
	if item == nil {
		return nil, apperr.New("UNAUTHENTICATED", "临时凭证无效")
	}
	if time.Now().After(item.ExpiresAt) {
		delete(m.byToken, item.Token)
		delete(m.byID, item.ID)
		return nil, apperr.New("TICKET_EXPIRED", "临时凭证已过期")
	}
	return item, nil
}

func consumeTicketLocked(item *Ticket) (Ticket, error) {
	if item.uses >= item.MaxUses {
		return Ticket{}, apperr.New("UNAUTHENTICATED", "临时凭证已被使用")
	}
	item.uses++
	return *item, nil
}

func (item Ticket) validateSource(source string) error {
	if item.ClientIP == "" || item.ClientIP == source {
		return nil
	}
	return apperr.New("PERMISSION_DENIED", "临时凭证不允许当前来源 IP 使用")
}

func normalizeClientIP(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil || !address.IsValid() {
		return "", apperr.New("INVALID_TICKET", "临时凭证来源 IP 无效")
	}
	return address.Unmap().String(), nil
}

func ticketPathAllowed(item Ticket, requestPath string) bool {
	for _, allowed := range item.Paths {
		if allowed == requestPath {
			return true
		}
	}
	if !item.PathPrefix {
		return false
	}
	return item.Path == "." || requestPath == item.Path || strings.HasPrefix(requestPath, item.Path+"/")
}

func (t Ticket) AllowsPath(requestPath string) bool {
	cleanPath, err := normalizePath(requestPath)
	return err == nil && ticketPathAllowed(t, cleanPath)
}

func normalizePath(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" {
		value = "."
	}
	if strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return "", apperr.New("PATH_ESCAPE", "文件路径无效")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", apperr.New("PATH_ESCAPE", "文件路径越出工作目录")
		}
	}
	clean := path.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", apperr.New("PATH_ESCAPE", "文件路径越出工作目录")
	}
	return clean, nil
}

func (m *Manager) Revoke(ticketID string) {
	m.mu.Lock()
	if item := m.byID[ticketID]; item != nil {
		delete(m.byID, item.ID)
		delete(m.byToken, item.Token)
	}
	m.mu.Unlock()
}

func (m *Manager) RevokeSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	for token, item := range m.byToken {
		if item.SessionID == sessionID {
			delete(m.byToken, token)
			delete(m.byID, item.ID)
		}
	}
	m.mu.Unlock()
}

func (m *Manager) removeExpiredLocked(now time.Time) {
	for token, item := range m.byToken {
		if now.After(item.ExpiresAt) {
			delete(m.byToken, token)
			delete(m.byID, item.ID)
		}
	}
}

func randomValue(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
