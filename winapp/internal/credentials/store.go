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
	// ClearAll 删除指定面板 URL 范围下的全部已保存账号与自动登录标记（不触碰其他面板/游戏账号）。
	ClearAll(panelURL string) error
	AutoLoginAccount(panelURL string) (string, error)
	SetAutoLoginAccount(panelURL, accountID string) error
}
