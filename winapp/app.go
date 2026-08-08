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

	netEaseMu      sync.Mutex
	netEaseClient  *game.Client
	netEaseAccount game.AccountState

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
	runtimeErr := game.InstallNetEaseRuntime(ctx)
	serviceErr := a.service.Start(ctx)
	if err := errors.Join(runtimeErr, serviceErr); err != nil {
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
	a.netEaseMu.Lock()
	defer a.netEaseMu.Unlock()
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
	a.netEaseClient = client
	a.netEaseAccount = account
	return account.Summary(), nil
}

func (a *App) DeleteNetEaseAccount() error {
	a.netEaseMu.Lock()
	defer a.netEaseMu.Unlock()
	if err := game.NewLocalAccountStore().Delete(); err != nil {
		return err
	}
	a.netEaseClient = nil
	a.netEaseAccount = game.AccountState{}
	return nil
}
func (a *App) GameVersions() []game.VersionOption { return game.SupportedVersions() }

func (a *App) GameServers() ([]game.ServerConfig, error) {
	store, err := game.DefaultServerStore()
	if err != nil {
		return nil, err
	}
	return store.List()
}

func (a *App) CreateGameServer(input game.ServerConfigInput) (game.ServerConfig, error) {
	ctx, cancel := a.operationContext(90 * time.Second)
	defer cancel()
	client, err := a.loginSavedNetEaseClient(ctx)
	if err != nil {
		return game.ServerConfig{}, err
	}
	detail, err := client.FetchNetGameDetail(ctx, input.GameID)
	if err != nil {
		return game.ServerConfig{}, err
	}
	input.GameID = detail.GameID
	input.Version = detail.Version
	store, err := game.DefaultServerStore()
	if err != nil {
		return game.ServerConfig{}, err
	}
	return store.Create(input)
}

func (a *App) UpdateGameServer(id string, input game.ServerConfigInput) (game.ServerConfig, error) {
	ctx, cancel := a.operationContext(90 * time.Second)
	defer cancel()
	client, err := a.loginSavedNetEaseClient(ctx)
	if err != nil {
		return game.ServerConfig{}, err
	}
	detail, err := client.FetchNetGameDetail(ctx, input.GameID)
	if err != nil {
		return game.ServerConfig{}, err
	}
	input.GameID = detail.GameID
	input.Version = detail.Version
	store, err := game.DefaultServerStore()
	if err != nil {
		return game.ServerConfig{}, err
	}
	return store.Update(id, input)
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
	return a.startGameJoin(server, game.LaunchKindNetGame), nil
}

func (a *App) JoinGameServerConfig(input game.ServerConfigInput) (game.JoinProgress, error) {
	ctx, cancel := a.operationContext(90 * time.Second)
	defer cancel()
	client, err := a.loginSavedNetEaseClient(ctx)
	if err != nil {
		return game.JoinProgress{}, err
	}
	detail, err := client.FetchNetGameDetail(ctx, input.GameID)
	if err != nil {
		return game.JoinProgress{}, err
	}
	input.GameID = detail.GameID
	input.Version = detail.Version
	server, err := game.NewTransientNetworkGame(input)
	if err != nil {
		return game.JoinProgress{}, err
	}
	return a.startGameJoin(server, game.LaunchKindNetGame), nil
}

func (a *App) NetGameLaunchOptions(gameID string) (game.NetGameLaunchOptions, error) {
	ctx, cancel := a.operationContext(90 * time.Second)
	defer cancel()
	client, err := a.loginSavedNetEaseClient(ctx)
	if err != nil {
		return game.NetGameLaunchOptions{}, err
	}
	return client.FetchNetGameLaunchOptions(ctx, gameID)
}

func (a *App) DeleteNetGameCharacter(gameID, roleName string) ([]game.GameCharacter, error) {
	ctx, cancel := a.operationContext(90 * time.Second)
	defer cancel()
	client, err := a.loginSavedNetEaseClient(ctx)
	if err != nil {
		return nil, err
	}
	return client.DeleteGameCharacter(ctx, gameID, roleName)
}

func (a *App) startGameJoin(server game.ServerConfig, kind game.LaunchKind) game.JoinProgress {
	if running := a.GameServerRunning(server.ID); running {
		return game.JoinProgress{ServerID: server.ID, Status: game.JoinStatusDone, Message: "游戏已经在运行中", Percent: 100, Running: true}
	}
	ctx, _ := a.operationContext(30 * time.Minute)
	return a.joins.Start(ctx, server, func(taskCtx context.Context, server game.ServerConfig, report func(string, string, float64)) (game.LaunchResult, error) {
		client, account, err := a.loginSavedNetEaseSession(taskCtx)
		if err != nil {
			return game.LaunchResult{}, err
		}
		return game.PrepareJoinWithProgress(taskCtx, server, kind, client, account, a.processes, report)
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
	client, _, err := a.loginSavedNetEaseSession(ctx)
	return client, err
}

func (a *App) loginSavedNetEaseSession(ctx context.Context) (*game.Client, game.AccountState, error) {
	a.netEaseMu.Lock()
	defer a.netEaseMu.Unlock()
	if a.netEaseClient != nil {
		return a.netEaseClient, a.netEaseAccount, nil
	}
	account, err := a.loadNetEaseAccount()
	if err != nil {
		return nil, game.AccountState{}, err
	}
	client, err := game.NewClient(account)
	if err != nil {
		return nil, game.AccountState{}, err
	}
	fresh, err := client.Login(ctx)
	if err != nil {
		return nil, game.AccountState{}, err
	}
	if err := game.NewLocalAccountStore().Save(fresh); err != nil {
		return nil, game.AccountState{}, err
	}
	a.netEaseClient = client
	a.netEaseAccount = fresh
	return client, fresh, nil
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
