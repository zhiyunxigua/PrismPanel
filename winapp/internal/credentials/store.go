package credentials

import (
	"errors"
	"time"
)

var (
	ErrNotFound    = errors.New("saved account not found")
	ErrUnsupported = errors.New("saved accounts are unavailable on this platform")
)

type Account struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	LastLoginAt time.Time `json:"last_login_at"`
}

type Credential struct {
	Account
	Password string `json:"-"`
}

type Store interface {
	List(panelURL string) ([]Account, error)
	Get(panelURL, accountID string) (Credential, error)
	Save(panelURL, username, password string, lastLoginAt time.Time) (Account, error)
	Delete(panelURL, accountID string) error
	AutoLoginAccount(panelURL string) (string, error)
	SetAutoLoginAccount(panelURL, accountID string) error
}
