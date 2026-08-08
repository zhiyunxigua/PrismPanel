package firewall

import "time"

const (
	SchemaVersion    = 1
	DefaultGrantTTL  = 10 * time.Minute
	DefaultTicketTTL = time.Minute
)

type PortRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type RuleInput struct {
	Enabled   bool        `json:"enabled"`
	Protocols []string    `json:"protocols"`
	Ports     []PortRange `json:"ports"`
	Sources   []string    `json:"sources"`
	Note      string      `json:"note"`
}

type Rule struct {
	ID        string      `json:"id"`
	Enabled   bool        `json:"enabled"`
	Protocols []string    `json:"protocols"`
	Ports     []PortRange `json:"ports"`
	Sources   []string    `json:"sources"`
	Note      string      `json:"note"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

type SystemAccess struct {
	Enabled         bool     `json:"enabled"`
	ControlSources  []string `json:"control_sources"`
	GrantTTLSeconds int      `json:"grant_ttl_seconds"`
}

type SystemAccessInput struct {
	Enabled             bool     `json:"enabled"`
	ControlSources      []string `json:"control_sources"`
	GrantTTLSeconds     int      `json:"grant_ttl_seconds"`
	IncludeCallerSource bool     `json:"include_caller_source"`
}

type Grant struct {
	Source    string    `json:"source"`
	SessionID string    `json:"session_id,omitempty"`
	TicketID  string    `json:"ticket_id,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Status struct {
	Supported    bool         `json:"supported"`
	Reason       string       `json:"reason,omitempty"`
	OS           string       `json:"os"`
	Architecture string       `json:"architecture"`
	Backend      string       `json:"backend"`
	State        string       `json:"state"`
	Revision     int64        `json:"revision"`
	DesiredHash  string       `json:"desired_hash,omitempty"`
	ObservedHash string       `json:"observed_hash,omitempty"`
	TablePresent bool         `json:"table_present"`
	Drift        bool         `json:"drift"`
	LastApplied  *time.Time   `json:"last_applied_at,omitempty"`
	LastError    string       `json:"last_error,omitempty"`
	System       SystemAccess `json:"system"`
	GrantCount   int          `json:"grant_count"`
}

type View struct {
	Status Status  `json:"status"`
	Rules  []Rule  `json:"rules"`
	Grants []Grant `json:"grants"`
}

type CreateRuleInput struct {
	ExpectedRevision int64     `json:"expected_revision"`
	Rule             RuleInput `json:"rule"`
}

type UpdateRuleInput struct {
	ExpectedRevision int64     `json:"expected_revision"`
	Rule             RuleInput `json:"rule"`
}

type DeleteRuleInput struct {
	ExpectedRevision int64 `json:"expected_revision"`
}

type ConfigureSystemInput struct {
	ExpectedRevision int64             `json:"expected_revision"`
	System           SystemAccessInput `json:"system"`
}
