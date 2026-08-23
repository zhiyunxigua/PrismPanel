package game

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MCDownloadTask 一个下载队列任务（安装版本或安装 Fabric）。
type MCDownloadTask struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // "version" | "fabric"
	VersionID string    `json:"version_id"`
	Loader    string    `json:"loader,omitempty"`
	Status    string    `json:"status"` // queued | downloading | done | failed | canceled
	Stage     string    `json:"stage"`
	Message   string    `json:"message"`
	Percent   float64   `json:"percent"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	dedupKey string             `json:"-"`
	cancel   context.CancelFunc `json:"-"`
}

const (
	MCTaskQueued      = "queued"
	MCTaskDownloading = "downloading"
	MCTaskDone        = "done"
	MCTaskFailed      = "failed"
	MCTaskCanceled    = "canceled"
)

// mcDownloadMaxParallel 同时下载的版本数上限（其余排队）。
const mcDownloadMaxParallel = 3

var mcDownloads = NewMCDownloadManager()

// MCDownloadManager 版本下载队列：入队后按顺序调度，最多 mcDownloadMaxParallel 个任务并行。
type MCDownloadManager struct {
	mu      sync.Mutex
	tasks   map[string]*MCDownloadTask
	order   []string
	running int
	emitter func(MCDownloadTask)
}

func NewMCDownloadManager() *MCDownloadManager {
	return &MCDownloadManager{tasks: map[string]*MCDownloadTask{}}
}

// SetEmitter 设置任务更新回调（前端实时推送）。
func (m *MCDownloadManager) SetEmitter(fn func(MCDownloadTask)) {
	m.mu.Lock()
	m.emitter = fn
	m.mu.Unlock()
}

func (m *MCDownloadManager) emit(task *MCDownloadTask) {
	m.mu.Lock()
	emitter := m.emitter
	copy := *task
	m.mu.Unlock()
	if emitter != nil {
		emitter(copy)
	}
}

// Add 加入下载队列；kind 为 "version" 或 "fabric"。
func (m *MCDownloadManager) Add(kind, versionID, loader string) (MCDownloadTask, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return MCDownloadTask{}, errors.New("版本不能为空")
	}
	if kind != "version" && kind != "fabric" {
		return MCDownloadTask{}, errors.New("未知的下载类型")
	}
	dedupKey := kind + "|" + versionID + "|" + strings.TrimSpace(loader)
	m.mu.Lock()
	for _, key := range m.order {
		if m.tasks[key].dedupKey == dedupKey {
			m.mu.Unlock()
			return MCDownloadTask{}, errors.New("该任务已在下载队列中")
		}
	}
	id, err := randomHex(12)
	if err != nil {
		m.mu.Unlock()
		return MCDownloadTask{}, err
	}
	task := &MCDownloadTask{
		ID: "dl-" + id, Kind: kind, VersionID: versionID, Loader: strings.TrimSpace(loader),
		Status: MCTaskQueued, Stage: "queue", Message: "等待下载",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), dedupKey: dedupKey,
	}
	m.tasks[task.ID] = task
	m.order = append(m.order, task.ID)
	copy := *task
	m.mu.Unlock()
	m.emit(&copy)
	m.schedule()
	return copy, nil
}

// List 按入队顺序返回所有任务。
func (m *MCDownloadManager) List() []MCDownloadTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MCDownloadTask, 0, len(m.order))
	for _, key := range m.order {
		if task, ok := m.tasks[key]; ok {
			out = append(out, *task)
		}
	}
	return out
}

// ActiveCount 排队中 + 下载中的任务数。
func (m *MCDownloadManager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, key := range m.order {
		if task := m.tasks[key]; task.Status == MCTaskQueued || task.Status == MCTaskDownloading {
			count++
		}
	}
	return count
}

// Cancel 取消排队中或下载中的任务。
func (m *MCDownloadManager) Cancel(id string) error {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return errors.New("下载任务不存在")
	}
	switch task.Status {
	case MCTaskQueued:
		task.Status = MCTaskCanceled
		task.Stage = "canceled"
		task.Message = "已取消"
	case MCTaskDownloading:
		task.Status = MCTaskCanceled
		task.Stage = "canceled"
		task.Message = "正在取消…"
		cancel := task.cancel
		m.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil
	default:
		m.mu.Unlock()
		return errors.New("任务已完成或已取消，无法再次取消")
	}
	copy := *task
	m.mu.Unlock()
	m.emit(&copy)
	return nil
}

// Remove 移除已完成/失败/已取消的任务。
func (m *MCDownloadManager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return errors.New("下载任务不存在")
	}
	if task.Status == MCTaskQueued || task.Status == MCTaskDownloading {
		return errors.New("任务仍在进行中，请先取消")
	}
	m.removeLocked(id)
	return nil
}

// ClearFinished 清空所有已完成/失败/已取消的任务。
func (m *MCDownloadManager) ClearFinished() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range m.order {
		task := m.tasks[key]
		if task.Status == MCTaskDone || task.Status == MCTaskFailed || task.Status == MCTaskCanceled {
			delete(m.tasks, key)
		}
	}
	keep := m.order[:0]
	for _, key := range m.order {
		if _, ok := m.tasks[key]; ok {
			keep = append(keep, key)
		}
	}
	m.order = keep
}

func (m *MCDownloadManager) removeLocked(id string) {
	delete(m.tasks, id)
	for index, key := range m.order {
		if key == id {
			m.order = append(m.order[:index], m.order[index+1:]...)
			break
		}
	}
}

// schedule 启动排队中的任务（不超过并发上限）。
func (m *MCDownloadManager) schedule() {
	m.mu.Lock()
	for m.running < mcDownloadMaxParallel {
		var next *MCDownloadTask
		for _, key := range m.order {
			if task := m.tasks[key]; task.Status == MCTaskQueued {
				next = task
				break
			}
		}
		if next == nil {
			break
		}
		next.Status = MCTaskDownloading
		next.Stage = "prepare"
		next.Message = "准备下载"
		ctx, cancel := context.WithCancel(context.Background())
		next.cancel = cancel
		m.running++
		id := next.ID
		m.mu.Unlock()
		m.emit(next)
		go m.run(ctx, id)
		m.mu.Lock()
	}
	m.mu.Unlock()
}

// mcDownloadRunner 实际执行下载任务（测试可注入假实现）。
var mcDownloadRunner = func(ctx context.Context, task *MCDownloadTask, report func(stage, message string, percent float64)) error {
	if task.Kind == "fabric" {
		_, err := InstallMCFabric(ctx, task.VersionID, task.Loader, report)
		return err
	}
	return InstallMCVersion(ctx, task.VersionID, report)
}

// run 执行单个下载任务。
func (m *MCDownloadManager) run(ctx context.Context, id string) {
	m.mu.Lock()
	task := m.tasks[id]
	m.mu.Unlock()
	if task == nil {
		return
	}
	defer task.cancel()

	report := func(stage, message string, percent float64) {
		m.mu.Lock()
		if task.Status == MCTaskCanceled {
			m.mu.Unlock()
			return
		}
		task.Stage = stage
		task.Message = message
		task.Percent = percent
		task.UpdatedAt = time.Now().UTC()
		m.mu.Unlock()
		m.emit(task)
	}

	err := mcDownloadRunner(ctx, task, report)

	m.mu.Lock()
	if task.Status == MCTaskCanceled {
		task.Message = "已取消"
	} else {
		switch {
		case err == nil:
			task.Status = MCTaskDone
			task.Stage = "done"
			task.Message = "下载完成"
			task.Percent = 100
		case ctx.Err() != nil:
			task.Status = MCTaskCanceled
			task.Stage = "canceled"
			task.Message = "已取消"
		default:
			task.Status = MCTaskFailed
			task.Stage = "failed"
			task.Message = "下载失败"
			task.Error = fmt.Sprintf("%v", err)
		}
	}
	task.UpdatedAt = time.Now().UTC()
	copy := *task
	m.running--
	m.mu.Unlock()
	m.emit(&copy)
	m.schedule()
}

// ---- 包级入口（供 app.go 绑定）----

func MCDownloadAdd(kind, versionID, loader string) (MCDownloadTask, error) {
	return mcDownloads.Add(kind, versionID, loader)
}

func MCDownloadList() []MCDownloadTask { return mcDownloads.List() }

func MCDownloadActiveCount() int { return mcDownloads.ActiveCount() }

func MCDownloadCancel(id string) error { return mcDownloads.Cancel(id) }

func MCDownloadRemove(id string) error { return mcDownloads.Remove(id) }

func MCDownloadClearFinished() { mcDownloads.ClearFinished() }

func SetMCDownloadEmitter(fn func(MCDownloadTask)) { mcDownloads.SetEmitter(fn) }
