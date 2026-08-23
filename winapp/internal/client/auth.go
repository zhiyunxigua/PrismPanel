package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type LoginResult struct {
	User              map[string]any `json:"user"`
	Initialized       bool           `json:"initialized"`
	CredentialWarning string         `json:"credential_warning,omitempty"`
}

func (c *Client) Login(ctx context.Context, username, password string) (LoginResult, error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return LoginResult{}, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.runtime.APIBaseURL+"/api/v1/auth/login", bytes.NewReader(body),
	)
	if err != nil {
		return LoginResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Prism-Client-Session", c.runtime.ProxySession)
	request.Header.Set("User-Agent", "PrismPanel-WinApp")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return LoginResult{}, fmt.Errorf("登录远程 Panel: %w", err)
	}
	defer response.Body.Close()
	var payload struct {
		Success bool        `json:"success"`
		Data    LoginResult `json:"data"`
		Error   *APIError   `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return LoginResult{}, fmt.Errorf("解析登录响应: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !payload.Success {
		if payload.Error != nil {
			return LoginResult{}, payload.Error
		}
		return LoginResult{}, errors.New("远程 Panel 登录失败")
	}
	if payload.Data.User == nil {
		return LoginResult{}, errors.New("远程 Panel 未返回登录用户")
	}
	return payload.Data, nil
}
