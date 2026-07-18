package main

import (
	"context"
	"sync"
	"time"

	"PrismPanel-winapp/internal/application"
	"PrismPanel-winapp/internal/client"
	"PrismPanel-winapp/internal/credentials"
	"PrismPanel-winapp/internal/settings"
)

type App struct {
	service *application.Service

	mu          sync.Mutex
	ctx         context.Context
	startErr    string
	startupDone chan struct{}
	startupOnce sync.Once
}

func newApp() (*App, error) {
	settingsPath, err := settings.DefaultPath()
	if err != nil {
		return nil, err
	}
	return &App{
		service:     application.New(settings.NewStore(settingsPath), credentials.NewStore()),
		startupDone: make(chan struct{}),
	}, nil
}

func (a *App) startup(ctx context.Context) {
	defer func() {
		a.startupOnce.Do(func() { close(a.startupDone) })
	}()
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
	a.waitForStartup()
	runtime := a.service.RuntimeConfig()
	a.mu.Lock()
	if a.startErr != "" {
		runtime.ConnectionErr = a.startErr
	}
	a.mu.Unlock()
	return runtime
}

func (a *App) waitForStartup() {
	if a.startupDone == nil {
		return
	}
	select {
	case <-a.startupDone:
		return
	default:
		<-a.startupDone
	}
}

func (a *App) ConfigurePanelURL(panelURL string) (application.RuntimeConfig, error) {
	configureContext, cancel := a.operationContext(15 * time.Second)
	defer cancel()
	runtime, err := a.service.ConfigurePanelURL(configureContext, panelURL)
	if err == nil {
		a.mu.Lock()
		a.startErr = ""
		a.mu.Unlock()
	}
	return runtime, err
}

func (a *App) SavedAccounts() ([]credentials.Account, error) {
	return a.service.SavedAccounts()
}

func (a *App) Login(username, password string, remember bool) (client.LoginResult, error) {
	ctx, cancel := a.operationContext(30 * time.Second)
	defer cancel()
	return a.service.Login(ctx, username, password, remember)
}

func (a *App) LoginSavedAccount(accountID string) (client.LoginResult, error) {
	ctx, cancel := a.operationContext(30 * time.Second)
	defer cancel()
	return a.service.LoginSavedAccount(ctx, accountID)
}

func (a *App) DeleteSavedAccount(accountID string) ([]credentials.Account, error) {
	return a.service.DeleteSavedAccount(accountID)
}

func (a *App) UpdateSavedPassword(username, password string) (bool, error) {
	return a.service.UpdateSavedPassword(username, password)
}

func (a *App) operationContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, timeout)
}

func (a *App) shutdown(ctx context.Context) {
	closeContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = a.service.Close(closeContext)
}
