package client

import (
	"context"
	"fmt"
	"net/url"

	"PrismPanel-winapp/internal/proxy"
)

type RuntimeConfig struct {
	Mode         string `json:"mode"`
	APIBaseURL   string `json:"apiBaseUrl"`
	ProxySession string `json:"proxySession"`
}

type Client struct {
	proxy   *proxy.Server
	runtime RuntimeConfig
}

func New(panelURL string) (*Client, error) {
	target, err := url.Parse(panelURL)
	if err != nil {
		return nil, fmt.Errorf("parse panel URL: %w", err)
	}
	panelProxy, err := proxy.New(proxy.Config{Target: target})
	if err != nil {
		return nil, err
	}
	sessionID, err := panelProxy.NewSession()
	if err != nil {
		return nil, err
	}
	return &Client{
		proxy:   panelProxy,
		runtime: RuntimeConfig{Mode: "winapp", ProxySession: sessionID},
	}, nil
}

func (c *Client) Start() error {
	if err := c.proxy.Start(); err != nil {
		return err
	}
	c.runtime.APIBaseURL = c.proxy.URL()
	return nil
}

func (c *Client) Close(ctx context.Context) error {
	return c.proxy.Close(ctx)
}

func (c *Client) RuntimeConfig() RuntimeConfig {
	return c.runtime
}
