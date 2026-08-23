package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"PrismPanel-winapp/internal/client"
	"PrismPanel-winapp/internal/credentials"
)

func (s *Service) SavedAccounts() ([]credentials.Account, error) {
	panelURL, err := s.configuredPanelURL()
	if err != nil {
		return nil, err
	}
	return s.credentials.List(panelURL)
}

func (s *Service) Login(
	ctx context.Context,
	username string,
	password string,
	remember bool,
) (client.LoginResult, error) {
	panelClient, panelURL, err := s.connectedClient()
	if err != nil {
		return client.LoginResult{}, err
	}
	result, err := panelClient.Login(ctx, username, password)
	if err != nil {
		return client.LoginResult{}, err
	}
	if remember {
		if saveErr := s.persistSuccessfulLogin(panelURL, result, password); saveErr != nil {
			result.CredentialWarning = "已登录，但保存 Windows 登录凭据失败：" + saveErr.Error()
		}
	} else if clearErr := s.credentials.SetAutoLoginAccount(panelURL, ""); clearErr != nil {
		result.CredentialWarning = "已登录，但无法关闭下次启动时的自动登录：" + clearErr.Error()
	}
	return result, nil
}

func (s *Service) LoginSavedAccount(ctx context.Context, accountID string) (client.LoginResult, error) {
	panelClient, panelURL, err := s.connectedClient()
	if err != nil {
		return client.LoginResult{}, err
	}
	return s.loginSavedAccount(ctx, panelURL, accountID, panelClient)
}

func (s *Service) DeleteSavedAccount(accountID string) ([]credentials.Account, error) {
	panelURL, err := s.configuredPanelURL()
	if err != nil {
		return nil, err
	}
	if err := s.credentials.Delete(panelURL, accountID); err != nil {
		return nil, err
	}
	return s.credentials.List(panelURL)
}

func (s *Service) UpdateSavedPassword(username, password string) (bool, error) {
	panelURL, err := s.configuredPanelURL()
	if err != nil {
		return false, err
	}
	accounts, err := s.credentials.List(panelURL)
	if err != nil {
		return false, err
	}
	for _, account := range accounts {
		if !strings.EqualFold(account.Username, strings.TrimSpace(username)) {
			continue
		}
		if _, err := s.credentials.Save(panelURL, account.Username, password, account.LastLoginAt); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (s *Service) autoLogin(ctx context.Context, panelURL string, panelClient *client.Client) error {
	accountID, err := s.credentials.AutoLoginAccount(panelURL)
	if err != nil || accountID == "" {
		return err
	}
	_, err = s.loginSavedAccount(ctx, panelURL, accountID, panelClient)
	if errors.Is(err, credentials.ErrNotFound) {
		_ = s.credentials.SetAutoLoginAccount(panelURL, "")
		return nil
	}
	if err == nil {
		return nil
	}
	var apiError *client.APIError
	if errors.As(err, &apiError) && apiError.Code == "INVALID_CREDENTIALS" {
		_ = s.credentials.SetAutoLoginAccount(panelURL, "")
		return errors.New("已保存账号的密码已失效，请重新输入密码")
	}
	return fmt.Errorf("自动登录失败：%w", err)
}

func (s *Service) loginSavedAccount(
	ctx context.Context,
	panelURL string,
	accountID string,
	panelClient *client.Client,
) (client.LoginResult, error) {
	credential, err := s.credentials.Get(panelURL, accountID)
	if err != nil {
		return client.LoginResult{}, err
	}
	result, err := panelClient.Login(ctx, credential.Username, credential.Password)
	if err != nil {
		var apiError *client.APIError
		if errors.As(err, &apiError) && apiError.Code == "INVALID_CREDENTIALS" {
			current, currentErr := s.credentials.AutoLoginAccount(panelURL)
			if currentErr == nil && current == accountID {
				_ = s.credentials.SetAutoLoginAccount(panelURL, "")
			}
		}
		return client.LoginResult{}, err
	}
	if saveErr := s.persistSuccessfulLogin(panelURL, result, credential.Password); saveErr != nil {
		result.CredentialWarning = "已登录，但更新 Windows 登录凭据失败：" + saveErr.Error()
	}
	return result, nil
}

func (s *Service) persistSuccessfulLogin(panelURL string, result client.LoginResult, password string) error {
	username, lastLoginAt, err := loginIdentity(result)
	if err != nil {
		return err
	}
	account, err := s.credentials.Save(panelURL, username, password, lastLoginAt)
	if err != nil {
		return err
	}
	return s.credentials.SetAutoLoginAccount(panelURL, account.ID)
}

func (s *Service) connectedClient() (*client.Client, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil || !s.runtime.Configured || strings.TrimSpace(s.runtime.PanelURL) == "" {
		return nil, "", errors.New("Windows 客户端尚未连接 Panel")
	}
	return s.client, s.runtime.PanelURL, nil
}

func (s *Service) configuredPanelURL() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.runtime.Configured || strings.TrimSpace(s.runtime.PanelURL) == "" {
		return "", errors.New("Windows 客户端尚未配置 Panel")
	}
	return s.runtime.PanelURL, nil
}

func loginIdentity(result client.LoginResult) (string, time.Time, error) {
	username, _ := result.User["username"].(string)
	username = strings.TrimSpace(username)
	if username == "" {
		return "", time.Time{}, errors.New("登录响应缺少用户名")
	}
	lastLoginAt := time.Now().UTC()
	if raw, ok := result.User["last_login_at"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			lastLoginAt = parsed.UTC()
		}
	}
	return username, lastLoginAt, nil
}
