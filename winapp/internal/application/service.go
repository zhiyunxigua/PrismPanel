package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"PrismPanel-winapp/internal/client"
	"PrismPanel-winapp/internal/proxy"
	"PrismPanel-winapp/internal/settings"
)

type RuntimeConfig struct {
	Mode          string `json:"mode"`
	Configured    bool   `json:"configured"`
	PanelURL      string `json:"panelUrl"`
	APIBaseURL    string `json:"apiBaseUrl"`
	ProxySession  string `json:"proxySession"`
	ConnectionErr string `json:"connectionError,omitempty"`
}

type Service struct {
	store settings.Store

	mu      sync.Mutex
	client  *client.Client
	runtime RuntimeConfig
}

func New(store settings.Store) *Service {
	return &Service{store: store, runtime: RuntimeConfig{Mode: "winapp"}}
}

func (s *Service) Start(ctx context.Context) error {
	value, err := s.store.Load()
	if err != nil {
		return err
	}
	if strings.TrimSpace(value.PanelURL) == "" {
		return nil
	}
	runtime, panelClient, err := s.connect(ctx, value.PanelURL)
	if err != nil {
		s.mu.Lock()
		s.runtime.PanelURL = value.PanelURL
		s.runtime.ConnectionErr = err.Error()
		s.mu.Unlock()
		return nil
	}
	s.mu.Lock()
	s.client = panelClient
	s.runtime = runtime
	s.mu.Unlock()
	return nil
}

func (s *Service) ConfigurePanelURL(ctx context.Context, rawURL string) (RuntimeConfig, error) {
	panelURL, err := NormalizePanelURL(rawURL)
	if err != nil {
		return RuntimeConfig{}, err
	}
	runtime, panelClient, err := s.connect(ctx, panelURL)
	if err != nil {
		return RuntimeConfig{}, err
	}
	if err := s.store.Save(settings.Settings{PanelURL: panelURL}); err != nil {
		_ = panelClient.Close(context.Background())
		return RuntimeConfig{}, err
	}

	s.mu.Lock()
	previous := s.client
	s.client = panelClient
	s.runtime = runtime
	s.mu.Unlock()
	if previous != nil {
		closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = previous.Close(closeContext)
		cancel()
	}
	return runtime, nil
}

func (s *Service) RuntimeConfig() RuntimeConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtime
}

func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	panelClient := s.client
	s.client = nil
	s.mu.Unlock()
	if panelClient == nil {
		return nil
	}
	return panelClient.Close(ctx)
}

func (s *Service) connect(ctx context.Context, panelURL string) (RuntimeConfig, *client.Client, error) {
	panelClient, err := client.New(panelURL)
	if err != nil {
		return RuntimeConfig{}, nil, err
	}
	if err := panelClient.Start(); err != nil {
		return RuntimeConfig{}, nil, err
	}
	clientRuntime := panelClient.RuntimeConfig()
	probeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := probePanel(probeContext, clientRuntime); err != nil {
		_ = panelClient.Close(context.Background())
		return RuntimeConfig{}, nil, err
	}
	return RuntimeConfig{
		Mode: "winapp", Configured: true, PanelURL: panelURL,
		APIBaseURL: clientRuntime.APIBaseURL, ProxySession: clientRuntime.ProxySession,
	}, panelClient, nil
}

func NormalizePanelURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("请输入完整的 Panel 根地址")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("Panel 地址必须使用 http 或 https")
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func probePanel(ctx context.Context, runtime client.RuntimeConfig) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, runtime.APIBaseURL+"/api/v1/auth/status", nil)
	if err != nil {
		return err
	}
	request.Header.Set(proxy.ClientSessionHeader, runtime.ProxySession)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("无法连接远程 Panel: %w", err)
	}
	defer response.Body.Close()
	var payload struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || !payload.Success {
		return errors.New("远程地址不是可用的 PrismPanel 服务")
	}
	return nil
}
