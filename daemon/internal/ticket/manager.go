package ticket

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"PrismPanel-daemon/internal/apperr"
)

type Ticket struct {
	ID         string    `json:"ticket_id"`
	Token      string    `json:"ticket"`
	Scope      string    `json:"scope"`
	InstanceID string    `json:"instance_id,omitempty"`
	ExpiresAt  time.Time `json:"expires_at"`
	MaxUses    int       `json:"max_uses"`
	uses       int
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
	if ttl <= 0 || ttl > 15*time.Minute {
		return Ticket{}, apperr.New("INVALID_TICKET", "临时凭证有效期必须在 15 分钟以内")
	}
	if maxUses <= 0 {
		maxUses = 1
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
	}
	m.mu.Lock()
	m.removeExpiredLocked(time.Now())
	m.byToken[token] = item
	m.byID[id] = item
	m.mu.Unlock()
	return *item, nil
}

func (m *Manager) Consume(token, scope, instanceID string) (Ticket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.byToken[token]
	if item == nil {
		return Ticket{}, apperr.New("UNAUTHENTICATED", "临时凭证无效")
	}
	if time.Now().After(item.ExpiresAt) {
		delete(m.byToken, item.Token)
		delete(m.byID, item.ID)
		return Ticket{}, apperr.New("TICKET_EXPIRED", "临时凭证已过期")
	}
	if item.Scope != scope || item.InstanceID != instanceID {
		return Ticket{}, apperr.New("PERMISSION_DENIED", "临时凭证不允许当前操作")
	}
	if item.uses >= item.MaxUses {
		return Ticket{}, apperr.New("UNAUTHENTICATED", "临时凭证已被使用")
	}
	item.uses++
	return *item, nil
}

func (m *Manager) Revoke(ticketID string) {
	m.mu.Lock()
	if item := m.byID[ticketID]; item != nil {
		delete(m.byID, item.ID)
		delete(m.byToken, item.Token)
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
