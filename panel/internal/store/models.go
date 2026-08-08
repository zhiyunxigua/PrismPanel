package store

import (
	"errors"
	"time"
)

const (
	GroupSuperAdmin = "super_admin"
	GroupAdmin      = "admin"
	GroupOperator   = "operator"
	GroupObserver   = "observer"

	UserActive   = "active"
	UserDisabled = "disabled"
	UserDeleted  = "deleted"
)

var (
	ErrNotFound       = errors.New("resource not found")
	ErrConflict       = errors.New("resource already exists")
	ErrLastSuperAdmin = errors.New("at least one active super administrator is required")
	ErrProtected      = errors.New("resource is protected")
	ErrInUse          = errors.New("resource is in use")
)

type User struct {
	ID             string           `json:"id"`
	Username       string           `json:"username"`
	DisplayName    string           `json:"display_name"`
	GroupCode      string           `json:"group_code"`
	Status         string           `json:"status"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	LastLoginAt    *time.Time       `json:"last_login_at,omitempty"`
	DeletedAt      *time.Time       `json:"-"`
	PasswordHash   string           `json:"-"`
	Permissions    []string         `json:"permissions,omitempty"`
	Group          UserGroupSummary `json:"group"`
	HasOverrides   bool             `json:"has_permission_overrides"`
	ActiveSessions int              `json:"active_sessions"`
}

func (u User) IsSuperAdmin() bool {
	return u.GroupCode == GroupSuperAdmin
}

type UserList struct {
	Items    []User `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

type UserFilter struct {
	Search   string
	Status   string
	Page     int
	PageSize int
}

type NewUser struct {
	ID           string
	Username     string
	DisplayName  string
	GroupCode    string
	PasswordHash string
}

type UserChanges struct {
	DisplayName string
	GroupCode   string
	Status      string
}

type Session struct {
	TokenHash     []byte
	User          User
	CreatedAt     time.Time
	LastSeenAt    time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}

type UserGroupSummary struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type UserGroup struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions []string  `json:"permissions"`
	BuiltIn     bool      `json:"built_in"`
	UserCount   int       `json:"user_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PermissionDefinition struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

type UserPermissionItem struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	GroupValue bool   `json:"group_value"`
	Effective  bool   `json:"effective"`
}

type UserPermissionProfile struct {
	Group       UserGroupSummary     `json:"group"`
	Permissions []UserPermissionItem `json:"permissions"`
}

type AuditLog struct {
	ID               string         `json:"id"`
	RequestID        string         `json:"request_id"`
	CreatedAt        time.Time      `json:"created_at"`
	ActorUserID      string         `json:"actor_user_id,omitempty"`
	SessionID        string         `json:"session_id,omitempty"`
	ActorUsername    string         `json:"actor_username"`
	ActorDisplayName string         `json:"actor_display_name"`
	SourceIP         string         `json:"source_ip"`
	UserAgent        string         `json:"user_agent"`
	Action           string         `json:"action"`
	ResourceType     string         `json:"resource_type"`
	ResourceID       string         `json:"resource_id"`
	ResourceName     string         `json:"resource_name"`
	RiskLevel        string         `json:"risk_level"`
	Success          bool           `json:"success"`
	ErrorCode        string         `json:"error_code,omitempty"`
	Detail           map[string]any `json:"detail,omitempty"`
}

type AuditList struct {
	Items    []AuditLog `json:"items"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
}

type Operator struct {
	UUID              string    `json:"uuid"`
	Name              string    `json:"name"`
	CreatedByUserID   string    `json:"created_by_user_id"`
	CreatedByUsername string    `json:"created_by_username"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type OperatorState struct {
	PanelID     string     `json:"panel_id"`
	Revision    uint64     `json:"revision"`
	Initialized bool       `json:"initialized"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Operators   []Operator `json:"operators"`
}

type FileOperation struct {
	ID               string         `json:"id"`
	RequestID        string         `json:"request_id"`
	CreatedAt        time.Time      `json:"created_at"`
	ExpiresAt        time.Time      `json:"expires_at"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
	ActorUserID      string         `json:"actor_user_id,omitempty"`
	SessionID        string         `json:"session_id,omitempty"`
	ActorUsername    string         `json:"actor_username"`
	ActorDisplayName string         `json:"actor_display_name"`
	SourceIP         string         `json:"source_ip"`
	UserAgent        string         `json:"user_agent"`
	Action           string         `json:"action"`
	NodeID           string         `json:"node_id"`
	ResourceType     string         `json:"resource_type"`
	ResourceID       string         `json:"resource_id"`
	Status           string         `json:"status"`
	ErrorCode        string         `json:"error_code,omitempty"`
	Detail           map[string]any `json:"detail,omitempty"`
}

type Node struct {
	ID              string     `json:"id"`
	DaemonID        string     `json:"daemon_id"`
	Name            string     `json:"name"`
	BaseURL         string     `json:"base_url"`
	PublicURL       string     `json:"public_url,omitempty"`
	TokenCiphertext []byte     `json:"-"`
	Enabled         bool       `json:"enabled"`
	DaemonVersion   string     `json:"daemon_version,omitempty"`
	ProtocolVersion string     `json:"protocol_version,omitempty"`
	Capabilities    []string   `json:"capabilities"`
	LastConnectedAt *time.Time `json:"last_connected_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
