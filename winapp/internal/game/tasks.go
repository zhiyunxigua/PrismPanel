package game

import (
	"context"
	"sync"
	"time"
)

// LaunchResult 一次启动的结果（国际版启动器与历史启动路径共用）。
type LaunchResult struct {
	Preparation JoinPreparation `json:"preparation"`
	JavaPath    string          `json:"java_path"`
	PID         int             `json:"pid"`
	LogPath     string          `json:"log_path"`
}

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

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
