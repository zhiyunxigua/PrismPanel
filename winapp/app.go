package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"PrismPanel-winapp/internal/application"
	"PrismPanel-winapp/internal/client"
	"PrismPanel-winapp/internal/credentials"
	"PrismPanel-winapp/internal/fileopen"
	"PrismPanel-winapp/internal/game"
	"PrismPanel-winapp/internal/settings"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	service   *application.Service
	joins     *game.JoinManager
	processes *game.ProcessManager
	files     *fileopen.Service

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
	cachePath, err := fileopen.DefaultCacheDir()
	if err != nil {
		return nil, err
	}
	app := &App{
		service:     application.New(settings.NewStore(settingsPath), credentials.NewStore()),
		joins:       game.NewJoinManager(),
		processes:   game.NewProcessManager(),
		startupDone: make(chan struct{}),
	}
	app.files = fileopen.New(cachePath, app.emitFileSyncEvent)
	return app, nil
}

func (a *App) startup(ctx context.Context) {
	defer func() { a.startupOnce.Do(func() { close(a.startupDone) }) }()
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	game.SetDevLogEmitter(func(entry game.DevLogEntry) {
		a.mu.Lock()
		current := a.ctx
		a.mu.Unlock()
		if current != nil {
			wailsRuntime.EventsEmit(current, "prism:dev-log", entry)
		}
	})
	game.SetMCDownloadEmitter(func(task game.MCDownloadTask) {
		a.mu.Lock()
		current := a.ctx
		a.mu.Unlock()
		if current != nil {
			wailsRuntime.EventsEmit(current, "prism:mc-download", task)
		}
	})
	a.files.Start(ctx)
	serviceErr := a.service.Start(ctx)
	if serviceErr != nil {
		a.mu.Lock()
		a.startErr = serviceErr.Error()
		a.mu.Unlock()
	}
}

func (a *App) OpenRemoteFile(input fileopen.Input, chooseApplication bool) (fileopen.OpenedFile, error) {
	runtime := a.service.RuntimeConfig()
	ctx, cancel := a.operationContext(30 * time.Minute)
	defer cancel()
	return a.files.Open(ctx, fileopen.Runtime{
		APIBaseURL: runtime.APIBaseURL, ProxySession: runtime.ProxySession,
	}, input, chooseApplication)
}

func (a *App) FileOpenLimit() int64 { return fileopen.MaxOpenFileSize }

func (a *App) emitFileSyncEvent(event fileopen.Event) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx != nil {
		wailsRuntime.EventsEmit(ctx, "prism:file-sync", event)
	}
}

func (a *App) RuntimeConfig() application.RuntimeConfig {
	a.waitForStartup()
	runtime := a.service.RuntimeConfig()
	runtime.Version = appVersion
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

// SelectMCGameDirectory 选择国际版游戏（版本存储）根目录。
func (a *App) SelectMCGameDirectory() (string, error) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return wailsRuntime.OpenDirectoryDialog(ctx, wailsRuntime.OpenDialogOptions{Title: "选择游戏目录（国际版版本存储根目录）"})
}

// SelectJavaExecutable 选择 Java 可执行文件。
func (a *App) SelectJavaExecutable() (string, error) {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return wailsRuntime.OpenFileDialog(ctx, wailsRuntime.OpenDialogOptions{
		Title:   "选择 Java 可执行文件（java.exe）",
		Filters: []wailsRuntime.FileFilter{{DisplayName: "Java 可执行文件", Pattern: "*.exe;*.bat;*.cmd"}},
	})
}

// PrepareGameInstance 准备国际版实例目录（mods/config/resourcepacks/shaderpacks）。
func (a *App) PrepareGameInstance(instanceDir string) error {
	return game.EnsureInstanceDirectories(instanceDir)
}

// ---- 国际版（Minecraft 国际版 / 离线）启动 ----

// devOp 执行一次操作并在开发者模式下记录日志。
func (a *App) devOp(kind, detail string, fn func() error) error {
	started := time.Now()
	err := fn()
	game.DevLog(kind, detail, time.Since(started), err)
	return err
}

// MCSetDevMode 开启/关闭开发者模式（所有操作记入 dev-mode.log）。
func (a *App) MCSetDevMode(on bool) (bool, error) {
	var enabled bool
	err := a.devOp("dev-mode", "设置开发者模式 "+map[bool]string{true: "开", false: "关"}[on], func() error {
		settings, err := game.LoadMCSettings()
		if err != nil {
			return err
		}
		settings.DevMode = on
		if err := game.SaveMCSettings(settings); err != nil {
			return err
		}
		enabled = game.DevModeEnabled()
		return nil
	})
	return enabled, err
}

// MCDevModeEnabled 当前开发者模式状态。
func (a *App) MCDevModeEnabled() bool { return game.DevModeEnabled() }

// MCDevLogList 返回最近的开发者日志。
func (a *App) MCDevLogList() []game.DevLogEntry { return game.DevLogList() }

// MCDevLogClear 清空开发者日志。
func (a *App) MCDevLogClear() error { return game.DevLogClear() }

// MCOpenDevLog 用系统默认程序打开开发者日志文件。
func (a *App) MCOpenDevLog() error { return game.OpenDevLog() }

// MCDevLogPath 返回开发者日志文件路径。
func (a *App) MCDevLogPath() string { return game.DevLogPath() }

// MCPushDevLog 前端通用操作日志：所有界面操作与反馈都通过这里写入日志文件。
func (a *App) MCPushDevLog(kind, detail string, elapsedMs int64, ok bool, errorText string) {
	if !game.DevModeEnabled() {
		return
	}
	var err error
	if !ok && errorText != "" {
		err = errors.New(errorText)
	}
	game.DevLog(kind, detail, time.Duration(elapsedMs)*time.Millisecond, err)
}

// MCAuthStatus 返回当前国际版账号（离线或微软）。
func (a *App) MCAuthStatus() (game.MCAccountSummary, error) {
	account, err := game.NewMCLocalStore().Load()
	if err != nil {
		if errors.Is(err, game.ErrMCNone) {
			return game.MCAccountSummary{}, nil
		}
		return game.MCAccountSummary{}, err
	}
	return account.Summary(), nil
}

// MCStartDeviceLogin 发起微软设备码登录。
func (a *App) MCStartDeviceLogin() (game.MCDeviceLogin, error) {
	var result game.MCDeviceLogin
	err := a.devOp("auth-device", "发起微软设备码登录", func() error {
		ctx, cancel := a.operationContext(60 * time.Second)
		defer cancel()
		var err error
		result, err = game.MCStartDeviceLogin(ctx)
		return err
	})
	return result, err
}

// MCPollDeviceLogin 轮询设备码登录结果；未完成时返回 authorization_pending 错误。
func (a *App) MCPollDeviceLogin(stateID string) (game.MCAccountSummary, error) {
	ctx, cancel := a.operationContext(30 * time.Second)
	defer cancel()
	account, err := game.MCPollDeviceLogin(ctx, stateID)
	if errors.Is(err, game.ErrAuthPending) {
		return game.MCAccountSummary{}, errors.New("authorization_pending")
	}
	if err != nil {
		return game.MCAccountSummary{}, err
	}
	return account.Summary(), nil
}

// MCSetOfflineAccount 设置离线账号。
func (a *App) MCSetOfflineAccount(name string) (game.MCAccountSummary, error) {
	var summary game.MCAccountSummary
	err := a.devOp("auth-offline", "设置离线账号 "+name, func() error {
		ctx, cancel := a.operationContext(10 * time.Second)
		defer cancel()
		account, err := game.MCSetOffline(ctx, name)
		if err != nil {
			return err
		}
		summary = account.Summary()
		return nil
	})
	return summary, err
}

// MCThirdPartyLogin 第三方认证服务器（authlib-injector / Yggdrasil）登录。
func (a *App) MCThirdPartyLogin(server, username, password string) (game.MCAccountSummary, error) {
	var summary game.MCAccountSummary
	err := a.devOp("auth-third-party", "第三方认证登录 "+server+" / "+username, func() error {
		ctx, cancel := a.operationContext(30 * time.Second)
		defer cancel()
		account, err := game.MCThirdPartyLogin(ctx, server, username, password)
		if err != nil {
			return err
		}
		summary = account.Summary()
		return nil
	})
	return summary, err
}

// MCLogout 删除本地国际版账号。
func (a *App) MCLogout() error {
	return a.devOp("auth-logout", "退出登录", func() error {
		return game.NewMCLocalStore().Delete()
	})
}

// MCAvailableVersions 拉取 Mojang 可用版本。
func (a *App) MCAvailableVersions() ([]game.MCVersionEntry, error) {
	ctx, cancel := a.operationContext(30 * time.Second)
	defer cancel()
	return game.FetchMCVersions(ctx)
}

// MCFabricLoaders 拉取指定版本可用的 Fabric Loader。
func (a *App) MCFabricLoaders(gameVersion string) ([]game.MCFabricLoader, error) {
	ctx, cancel := a.operationContext(30 * time.Second)
	defer cancel()
	return game.FetchMCFabricLoaders(ctx, gameVersion)
}

// MCInstalledVersions 列出本地已安装的国际版版本。
func (a *App) MCInstalledVersions() []game.MCInstalledVersion {
	versions, err := game.InstalledMCVersions()
	if err != nil {
		return nil
	}
	return versions
}

// MCDeleteVersion 删除指定版本（含独立的 .minecraft / mods 等）。
func (a *App) MCDeleteVersion(versionID string) error {
	return a.devOp("delete", "删除版本 "+versionID, func() error {
		return game.DeleteMCVersion(versionID)
	})
}

// MCIsFabricInstalled 判断指定版本是否已安装 Fabric Loader。
func (a *App) MCIsFabricInstalled(gameVersion string) (bool, error) {
	return game.MCFabricInstalled(gameVersion)
}

// MCGetVersionSettings 读取版本特定设置（不存在时返回空值）。
func (a *App) MCGetVersionSettings(versionID string) (game.MCVersionSettings, error) {
	settings, _, err := game.LoadMCVersionSettings(versionID)
	return settings, err
}

// MCSaveVersionSettings 保存版本特定设置。
func (a *App) MCSaveVersionSettings(versionID string, settings game.MCVersionSettings) error {
	return a.devOp("version-settings", "保存 "+versionID+" 启动设置", func() error {
		return game.SaveMCVersionSettings(versionID, settings)
	})
}

// MCGetLauncherSettings 读取全局下载/启动设置（并发数、默认镜像）。
func (a *App) MCGetLauncherSettings() (game.MCLauncherSettings, error) {
	return game.LoadMCSettings()
}

// MCSaveLauncherSettings 保存全局下载/启动设置。
func (a *App) MCSaveLauncherSettings(settings game.MCLauncherSettings) error {
	return a.devOp("launcher-settings", "保存下载/启动设置", func() error {
		return game.SaveMCSettings(settings)
	})
}

// MCInstallVersion 下载指定国际版版本（含 client/assets/libraries）。
func (a *App) MCInstallVersion(versionID string) (game.MCInstalledVersion, error) {
	var result game.MCInstalledVersion
	err := a.devOp("install", "安装版本 "+versionID, func() error {
		ctx, cancel := a.operationContext(60 * time.Minute)
		defer cancel()
		if err := game.InstallMCVersion(ctx, versionID, a.mcInstallReporter()); err != nil {
			result = game.MCInstalledVersion{ID: versionID}
			return err
		}
		result = game.MCInstalledVersion{ID: versionID, Installed: true}
		return nil
	})
	return result, err
}

// MCInstallFabric 为指定版本安装 Fabric Loader，返回生成的版本 ID。
func (a *App) MCInstallFabric(gameVersion, loaderVersion string) (game.MCInstalledVersion, error) {
	var result game.MCInstalledVersion
	err := a.devOp("fabric", "为 "+gameVersion+" 安装 Fabric "+loaderVersion, func() error {
		ctx, cancel := a.operationContext(30 * time.Minute)
		defer cancel()
		id, err := game.InstallMCFabric(ctx, gameVersion, loaderVersion, a.mcInstallReporter())
		if err != nil {
			result = game.MCInstalledVersion{ID: gameVersion, Fabric: true}
			return err
		}
		result = game.MCInstalledVersion{ID: id, Fabric: true, Installed: true}
		return nil
	})
	return result, err
}

// MCAddDownload 把下载任务加入队列（kind: version / fabric），后台多任务并行下载。
func (a *App) MCAddDownload(kind, versionID, loader string) (game.MCDownloadTask, error) {
	started := time.Now()
	task, err := game.MCDownloadAdd(kind, versionID, loader)
	game.DevLog("download-queue", fmt.Sprintf("加入下载队列 %s %s %s", kind, versionID, loader), time.Since(started), err)
	return task, err
}

// MCDownloadList 返回下载队列中的全部任务。
func (a *App) MCDownloadList() []game.MCDownloadTask { return game.MCDownloadList() }

// MCDownloadActiveCount 返回排队中 + 下载中的任务数。
func (a *App) MCDownloadActiveCount() int { return game.MCDownloadActiveCount() }

// MCCancelDownload 取消排队中或下载中的任务。
func (a *App) MCCancelDownload(id string) error {
	return a.devOp("download-queue", "取消下载 "+id, func() error {
		return game.MCDownloadCancel(id)
	})
}

// MCRemoveDownload 移除已完成/失败/已取消的任务。
func (a *App) MCRemoveDownload(id string) error {
	return a.devOp("download-queue", "移除下载任务 "+id, func() error {
		return game.MCDownloadRemove(id)
	})
}

// MCClearDownloads 清空已完成/失败/已取消的任务。
func (a *App) MCClearDownloads() {
	game.MCDownloadClearFinished()
}

// MCLaunch 启动国际版/离线客户端（异步，通过 MCLaunchProgress 查询状态）。
func (a *App) MCLaunch(input game.MCLaunchRequest) (game.JoinProgress, error) {
	account, err := game.NewMCLocalStore().Load()
	if err != nil {
		return game.JoinProgress{}, errors.New("请先设置国际版账号（离线或微软登录）")
	}
	server := game.ServerConfig{
		ID: "mc-" + safeMC(input.VersionID), IP: input.ServerIP, Port: input.ServerPort,
		Username: account.Name, VersionLabel: input.VersionID,
	}
	if server.Port <= 0 {
		server.Port = 25565
	}
	if server.IP == "" {
		server.IP = "127.0.0.1"
	}
	ctx, _ := a.operationContext(30 * time.Minute)
	progress := a.joins.Start(ctx, server, func(taskCtx context.Context, _ game.ServerConfig, report func(string, string, float64)) (game.LaunchResult, error) {
		started := time.Now()
		result, launchErr := game.LaunchMC(taskCtx, input, account, a.processes, report)
		game.DevLog("launch", fmt.Sprintf("启动 %s → %s:%d", input.VersionID, server.IP, server.Port), time.Since(started), launchErr)
		return result, launchErr
	})
	return progress, nil
}

// MCCloseGame 关闭指定国际版游戏进程。
func (a *App) MCCloseGame(id string) error {
	return a.devOp("close", "结束游戏 "+id, func() error {
		return a.processes.Close(id)
	})
}

// MCLaunchProgress 查询国际版启动状态。
func (a *App) MCLaunchProgress(id string) game.JoinProgress { return a.joins.Status(id) }

// MCModsList 列出指定版本已安装的 mod。
func (a *App) MCModsList(versionID string) ([]game.MCModEntry, error) {
	return game.MCModsList(versionID)
}

// MCModsToggle 启用/禁用指定 mod。
func (a *App) MCModsToggle(versionID, filename string, enabled bool) error {
	return a.devOp("mod", "切换 mod 状态 "+filename+" → "+map[bool]string{true: "启用", false: "禁用"}[enabled]+"（"+versionID+"）", func() error {
		return game.MCModsToggle(versionID, filename, enabled)
	})
}

// MCModsDelete 删除指定 mod。
func (a *App) MCModsDelete(versionID, filename string) error {
	return a.devOp("mod", "删除 mod "+filename+"（"+versionID+"）", func() error {
		return game.MCModsDelete(versionID, filename)
	})
}

// MCModsOpenDir 打开指定版本的 mods 目录。
func (a *App) MCModsOpenDir(versionID string) error {
	return a.devOp("mod", "打开 mods 目录（"+versionID+"）", func() error {
		return game.MCModsOpenDir(versionID)
	})
}

// MCSearchModrinth 在 Modrinth 搜索可用 mod。
func (a *App) MCSearchModrinth(query, gameVersion, loader string) ([]game.MCModrinthHit, error) {
	var hits []game.MCModrinthHit
	err := a.devOp("modrinth", "搜索 "+query+"（"+gameVersion+"/"+loader+"）", func() error {
		ctx, cancel := a.operationContext(30 * time.Second)
		defer cancel()
		var err error
		hits, err = game.MCSearchModrinth(ctx, query, gameVersion, loader)
		return err
	})
	return hits, err
}

// MCModrinthInstall 下载安装 Modrinth 项目的最新兼容版本到指定版本 mods 目录。
func (a *App) MCModrinthInstall(versionID, projectID, gameVersion, loader string) (string, error) {
	var filename string
	err := a.devOp("modrinth", "安装 "+projectID+" → "+versionID, func() error {
		ctx, cancel := a.operationContext(5 * time.Minute)
		defer cancel()
		var err error
		filename, err = game.MCModrinthInstall(ctx, versionID, projectID, gameVersion, loader)
		return err
	})
	return filename, err
}

func (a *App) mcInstallReporter() func(stage, message string, percent float64) {
	return func(stage, message string, percent float64) {
		a.mu.Lock()
		ctx := a.ctx
		a.mu.Unlock()
		if ctx != nil {
			wailsRuntime.EventsEmit(ctx, "prism:mc-install", map[string]any{
				"stage": stage, "message": message, "percent": percent,
			})
		}
	}
}

func safeMC(value string) string {
	return strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			return char
		}
		return '-'
	}, value)
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
