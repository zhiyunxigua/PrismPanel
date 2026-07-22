package game

import "time"

type AccountSummary struct {
	Email      string     `json:"email"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func (s AccountState) Summary() AccountSummary {
	return AccountSummary{Email: s.Email, VerifiedAt: s.VerifiedAt, UpdatedAt: s.VerifiedAtTime()}
}

func (s AccountState) VerifiedAtTime() time.Time {
	if s.VerifiedAt != nil {
		return s.VerifiedAt.UTC()
	}
	return time.Time{}
}

type LocalAccountStore interface {
	Load() (AccountState, error)
	Save(AccountState) error
	Delete() error
}

func NewLocalAccountStore() LocalAccountStore {
	return newLocalAccountStore()
}
