package game

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type JoinStatus string

const (
	JoinStatusIdle    JoinStatus = "idle"
	JoinStatusRunning JoinStatus = "running"
	JoinStatusDone    JoinStatus = "done"
	JoinStatusFailed  JoinStatus = "failed"
)

type JoinProgress struct {
	ServerID  string        `json:"server_id"`
	Status    JoinStatus    `json:"status"`
	Stage     string        `json:"stage"`
	Message   string        `json:"message"`
	Percent   float64       `json:"percent"`
	StartedAt time.Time     `json:"started_at,omitempty"`
	EndedAt   *time.Time    `json:"ended_at,omitempty"`
	Error     string        `json:"error,omitempty"`
	Result    *LaunchResult `json:"result,omitempty"`
	Running   bool          `json:"running"`
}

type JoinManager struct {
	mu    sync.Mutex
	tasks map[string]*joinTask
}

type joinTask struct {
	progress JoinProgress
	done     chan struct{}
}

func NewJoinManager() *JoinManager { return &JoinManager{tasks: make(map[string]*joinTask)} }

func (m *JoinManager) Status(serverID string) JoinProgress {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task := m.tasks[serverID]; task != nil {
		return task.progress
	}
	return JoinProgress{ServerID: serverID, Status: JoinStatusIdle, Message: "尚未开始"}
}

func (m *JoinManager) Start(ctx context.Context, server ServerConfig, prepare func(context.Context, ServerConfig, func(string, string, float64)) (LaunchResult, error)) JoinProgress {
	m.mu.Lock()
	if task := m.tasks[server.ID]; task != nil {
		progress := task.progress
		if progress.Status == JoinStatusRunning {
			m.mu.Unlock()
			return progress
		}
		delete(m.tasks, server.ID)
	}
	task := &joinTask{done: make(chan struct{})}
	task.progress = JoinProgress{ServerID: server.ID, Status: JoinStatusRunning, Stage: "queued", Message: "等待开始", Percent: 0, StartedAt: time.Now().UTC()}
	m.tasks[server.ID] = task
	m.mu.Unlock()

	go func() {
		defer close(task.done)
		setProgress := func(stage, message string, percent float64) {
			m.update(server.ID, func(progress *JoinProgress) {
				progress.Stage = stage
				progress.Message = message
				progress.Percent = clampPercent(percent)
			})
		}
		result, err := prepare(ctx, server, setProgress)
		now := time.Now().UTC()
		m.update(server.ID, func(progress *JoinProgress) {
			progress.EndedAt = &now
			if err != nil {
				progress.Status = JoinStatusFailed
				progress.Stage = "failed"
				progress.Message = "加入准备失败"
				progress.Error = err.Error()
				return
			}
			progress.Status = JoinStatusDone
			progress.Stage = "ready"
			progress.Message = "游戏启动成功"
			progress.Percent = 100
			progress.Result = &result
		})
	}()
	return m.Status(server.ID)
}

func (m *JoinManager) update(serverID string, fn func(*JoinProgress)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task := m.tasks[serverID]; task != nil {
		fn(&task.progress)
	}
}

func PrepareJoinWithProgress(ctx context.Context, server ServerConfig, client *Client, account AccountState, processes *ProcessManager, report func(stage, message string, percent float64)) (LaunchResult, error) {
	report("login", "网易账号已验证", 5)
	paths, err := DefaultCachePathsForVersion(server.VersionLabel)
	if err != nil {
		return LaunchResult{}, err
	}
	report("download", fmt.Sprintf("准备下载 %s", server.VersionLabel), 10)
	downloads, err := client.DownloadVersionPackagesWithProgress(ctx, server.Version, paths, func(label, phase string, itemIndex, itemCount int, current, total int64) {
		base := 10.0 + float64(itemIndex)*(60.0/float64(itemCount))
		part := 0.0
		if total > 0 {
			part = float64(current) / float64(total)
		} else if current > 0 {
			part = 0.5
		}
		percent := base + part*(60.0/float64(itemCount))
		stage := "download"
		verb := "下载中"
		if phase == "extract" {
			stage = "extract"
			verb = "解压中"
		} else if phase == "install" {
			stage = "install"
			verb = "安装中"
		} else if phase == "cached" {
			verb = "已缓存"
		}
		message := fmt.Sprintf("%s %.0f%%\uff1a%s", verb, clampPercent(percent), label)
		report(stage, message, percent)
	})
	if err != nil {
		return LaunchResult{}, err
	}
	report("extract", "解压/校验完成，正在复制运行目录", 75)
	prepared, err := PrepareServerRuntime(server)
	if err != nil {
		return LaunchResult{}, err
	}
	prepared.Downloads = downloads
	report("runtime", "运行目录已准备", 95)
	return LaunchPreparedGame(ctx, LaunchRequest{Server: server, Preparation: prepared, Account: account, ProtocolVersion: client.LauncherVersion()}, processes, report)
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
