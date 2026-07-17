package main

import (
	"context"
	"sync"
	"time"

	"PrismPanel-winapp/internal/application"
	"PrismPanel-winapp/internal/settings"
)

type App struct {
	service *application.Service

	mu       sync.Mutex
	ctx      context.Context
	startErr string
}

func newApp() (*App, error) {
	settingsPath, err := settings.DefaultPath()
	if err != nil {
		return nil, err
	}
	return &App{service: application.New(settings.NewStore(settingsPath))}, nil
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	if err := a.service.Start(ctx); err != nil {
		a.mu.Lock()
		a.startErr = err.Error()
		a.mu.Unlock()
	}
}

func (a *App) RuntimeConfig() application.RuntimeConfig {
	runtime := a.service.RuntimeConfig()
	a.mu.Lock()
	if a.startErr != "" {
		runtime.ConnectionErr = a.startErr
	}
	a.mu.Unlock()
	return runtime
}

func (a *App) ConfigurePanelURL(panelURL string) (application.RuntimeConfig, error) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	configureContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runtime, err := a.service.ConfigurePanelURL(configureContext, panelURL)
	if err == nil {
		a.mu.Lock()
		a.startErr = ""
		a.mu.Unlock()
	}
	return runtime, err
}

func (a *App) shutdown(ctx context.Context) {
	closeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = a.service.Close(closeContext)
}
