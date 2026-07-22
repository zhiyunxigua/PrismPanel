package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"PrismPanel-winapp/internal/application"
	"PrismPanel-winapp/internal/client"
	"PrismPanel-winapp/internal/credentials"
	"PrismPanel-winapp/internal/game"
	"PrismPanel-winapp/internal/settings"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	service   *application.Service
	joins     *game.JoinManager
	processes *game.ProcessManager

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
		joins:       game.NewJoinManager(),
		processes:   game.NewProcessManager(),
		startupDone: make(chan struct{}),
	}, nil
}

func (a *App) startup(ctx context.Context) {
	defer func() { a.startupOnce.Do(func() { close(a.startupDone) }) }()
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

func (a *App) SavedAccounts() ([]credentials.Account, error) { return a.service.SavedAccounts() }
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

func (a *App) NetEaseAccount() (game.AccountSummary, error) {
	account, err := a.loadNetEaseAccount()
	if errors.Is(err, game.ErrNotFound) {
		return game.AccountSummary{}, nil
	}
	if err != nil {
		return game.AccountSummary{}, err
	}
	return account.Summary(), nil
}

func (a *App) LoginNetEaseAccount(email, password string) (game.AccountSummary, error) {
	ctx, cancel := a.operationContext(90 * time.Second)
	defer cancel()
	client, err := game.NewClient(game.AccountState{Email: email, Password: password})
	if err != nil {
		return game.AccountSummary{}, err
	}
	account, err := client.Login(ctx)
	if err != nil {
		return game.AccountSummary{}, err
	}
	if err := game.NewLocalAccountStore().Save(account); err != nil {
		return game.AccountSummary{}, err
	}
	return account.Summary(), nil
}

func (a *App) DeleteNetEaseAccount() error        { return game.NewLocalAccountStore().Delete() }
func (a *App) GameVersions() []game.VersionOption { return game.SupportedVersions() }

func (a *App) GameServers() ([]game.ServerConfig, error) {
	store, err := game.DefaultServerStore()
	if err != nil {
		return nil, err
	}
	return store.List()
}

func (a *App) CreateGameServer(input game.ServerConfigInput) (game.ServerConfig, error) {
	store, err := game.DefaultServerStore()
	if err != nil {
		return game.ServerConfig{}, err
	}
	return store.Create(input)
}

func (a *App) DeleteGameServer(id string) ([]game.ServerConfig, error) {
	store, err := game.DefaultServerStore()
	if err != nil {
		return nil, err
	}
	return store.Delete(id)
}

func (a *App) SelectGameModDirectory() (string, error) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return wailsRuntime.OpenDirectoryDialog(ctx, wailsRuntime.OpenDialogOptions{Title: "选择自定义资源目录"})
}

func (a *App) JoinGameServer(id string) (game.JoinProgress, error) {
	store, err := game.DefaultServerStore()
	if err != nil {
		return game.JoinProgress{}, err
	}
	server, err := store.Get(id)
	if err != nil {
		return game.JoinProgress{}, err
	}
	return a.startGameJoin(server), nil
}

func (a *App) JoinGameServerConfig(input game.ServerConfigInput) (game.JoinProgress, error) {
	server, err := game.NewTransientServer(input)
	if err != nil {
		return game.JoinProgress{}, err
	}
	return a.startGameJoin(server), nil
}

func (a *App) startGameJoin(server game.ServerConfig) game.JoinProgress {
	if running := a.GameServerRunning(server.ID); running {
		return game.JoinProgress{ServerID: server.ID, Status: game.JoinStatusDone, Message: "游戏已经在运行中", Percent: 100, Running: true}
	}
	ctx, _ := a.operationContext(30 * time.Minute)
	return a.joins.Start(ctx, server, func(taskCtx context.Context, server game.ServerConfig, report func(string, string, float64)) (game.LaunchResult, error) {
		account, err := a.loadNetEaseAccount()
		if err != nil {
			return game.LaunchResult{}, err
		}
		client, err := game.NewClient(account)
		if err != nil {
			return game.LaunchResult{}, err
		}
		fresh, err := client.Login(taskCtx)
		if err != nil {
			return game.LaunchResult{}, err
		}
		_ = game.NewLocalAccountStore().Save(fresh)
		return game.PrepareJoinWithProgress(taskCtx, server, client, fresh, a.processes, report)
	})
}
func (a *App) GameJoinProgress(id string) game.JoinProgress { return a.joins.Status(id) }
func (a *App) GameServerRunning(id string) bool             { return a.processes.Running(id) }

func (a *App) CheckNetEaseGameVersion(email, password string, version game.Version) (game.MinecraftClientLibs, error) {
	ctx, cancel := a.operationContext(90 * time.Second)
	defer cancel()
	client, err := game.NewClient(game.AccountState{Email: email, Password: password})
	if err != nil {
		return game.MinecraftClientLibs{}, err
	}
	if _, err := client.Login(ctx); err != nil {
		return game.MinecraftClientLibs{}, err
	}
	return client.FetchMinecraftClientLibs(ctx, version)
}

func (a *App) DownloadNetEaseGameVersion(email, password string, version game.Version) ([]game.PackageDownload, error) {
	ctx, cancel := a.operationContext(30 * time.Minute)
	defer cancel()
	client, err := game.NewClient(game.AccountState{Email: email, Password: password})
	if err != nil {
		return nil, err
	}
	if _, err := client.Login(ctx); err != nil {
		return nil, err
	}
	label, err := game.VersionLabel(version)
	if err != nil {
		return nil, err
	}
	paths, err := game.DefaultCachePathsForVersion(label)
	if err != nil {
		return nil, err
	}
	return client.DownloadVersionPackages(ctx, version, paths)
}

func (a *App) CheckSavedNetEaseGameVersion(version game.Version) (game.MinecraftClientLibs, error) {
	ctx, cancel := a.operationContext(90 * time.Second)
	defer cancel()
	client, err := a.loginSavedNetEaseClient(ctx)
	if err != nil {
		return game.MinecraftClientLibs{}, err
	}
	return client.FetchMinecraftClientLibs(ctx, version)
}

func (a *App) DownloadSavedNetEaseGameVersion(version game.Version) ([]game.PackageDownload, error) {
	ctx, cancel := a.operationContext(30 * time.Minute)
	defer cancel()
	client, err := a.loginSavedNetEaseClient(ctx)
	if err != nil {
		return nil, err
	}
	label, err := game.VersionLabel(version)
	if err != nil {
		return nil, err
	}
	paths, err := game.DefaultCachePathsForVersion(label)
	if err != nil {
		return nil, err
	}
	return client.DownloadVersionPackages(ctx, version, paths)
}

func (a *App) PrepareGameInstance(instanceDir string) error {
	return game.EnsureInstanceDirectories(instanceDir)
}

func (a *App) SupportedGameVersionLabels() []string {
	versions := game.SupportedVersions()
	labels := make([]string, 0, len(versions))
	for _, item := range versions {
		labels = append(labels, item.Label)
	}
	return labels
}

func (a *App) loadNetEaseAccount() (game.AccountState, error) {
	return game.NewLocalAccountStore().Load()
}

func (a *App) loginSavedNetEaseClient(ctx context.Context) (*game.Client, error) {
	account, err := a.loadNetEaseAccount()
	if err != nil {
		return nil, err
	}
	client, err := game.NewClient(account)
	if err != nil {
		return nil, err
	}
	fresh, err := client.Login(ctx)
	if err != nil {
		return nil, err
	}
	_ = game.NewLocalAccountStore().Save(fresh)
	return client, nil
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
