package playerdata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type Error struct {
	Code       string
	Message    string
	StatusCode int
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "PlayerData 请求失败"
	}
	return e.Message
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Send(ctx context.Context, input any) (json.RawMessage, error) {
	if c == nil || c.baseURL == "" || c.token == "" {
		return nil, errors.New("PlayerData 邮件客户端未配置")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode PlayerData mail request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/mail/send", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create PlayerData mail request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request PlayerData mail service: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read PlayerData mail response: %w", err)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, &Error{Code: "INVALID_RESPONSE", Message: "PlayerData 返回格式无效", StatusCode: response.StatusCode}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		return nil, &Error{Code: envelope.Error.Code, Message: envelope.Error.Message, StatusCode: response.StatusCode}
	}
	return envelope.Data, nil
}
