package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/supervisor"
)

type Status string

const (
	StatusQueued              Status = "queued"
	StatusRunning             Status = "running"
	StatusCancelRequested     Status = "cancel_requested"
	StatusForceStopRequested  Status = "force_stop_requested"
	StatusCancelled           Status = "cancelled"
	StatusForceStopped        Status = "force_stopped"
	StatusCompleted           Status = "completed"
	StatusCompletedWithErrors Status = "completed_with_errors"
	StatusFailed              Status = "failed"
)

type Log struct {
	Sequence   uint64    `json:"sequence"`
	Timestamp  time.Time `json:"timestamp"`
	Level      string    `json:"level"`
	Stage      string    `json:"stage"`
	InstanceID string    `json:"instance_id,omitempty"`
	Message    string    `json:"message"`
}

type Snapshot struct {
	TaskID          string     `json:"task_id"`
	ServerID        string     `json:"server_id"`
	Targets         []int      `json:"targets"`
	Status          Status     `json:"status"`
	CurrentInstance string     `json:"current_instance,omitempty"`
	Completed       int        `json:"completed"`
	Failed          int        `json:"failed"`
	CopyStage       string     `json:"copy_stage,omitempty"`
	CopyConcurrency int        `json:"copy_concurrency"`
	CopyFilesTotal  int64      `json:"copy_files_total"`
	CopyFilesDone   int64      `json:"copy_files_done"`
	CopyBytesTotal  int64      `json:"copy_bytes_total"`
	CopyBytesDone   int64      `json:"copy_bytes_done"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Error           string     `json:"error,omitempty"`
	Logs            []Log      `json:"logs"`
}

type task struct {
	Snapshot
	context context.Context
	cancel  context.CancelFunc
	force   bool
	release func()
}

type Manager struct {
	servers         *serverservice.Service
	supervisor      *supervisor.Manager
	copyConcurrency int
	mu              sync.RWMutex
	tasks           map[string]*task
	active          map[string]string
	imageLocks      sync.Map
}

func NewManager(servers *serverservice.Service, processManager *supervisor.Manager, copyConcurrency int) *Manager {
	return &Manager{
		servers: servers, supervisor: processManager,
		copyConcurrency: copyConcurrency,
		tasks:           make(map[string]*task), active: make(map[string]string),
	}
}

func (m *Manager) Start(serverID string, requested []int) (Snapshot, error) {
	cfg, err := m.servers.Get(serverID)
	if err != nil {
		return Snapshot{}, err
	}
	if cfg.Type != "mirror" {
		return Snapshot{}, apperr.New("INVALID_STATE", "只有镜像服务器组可以执行镜像部署")
	}
	imageLock := m.imageLock(serverID)
	imageLock.RLock()
	imageLocked := true
	defer func() {
		if imageLocked {
			imageLock.RUnlock()
		}
	}()
	targets, err := normalizeTargets(requested, cfg.InstanceCount)
	if err != nil {
		return Snapshot{}, err
	}
	imagePath := filepath.Join(cfg.RootPath, cfg.ImageDirectory)
	info, err := os.Stat(imagePath)
	if err != nil || !info.IsDir() {
		return Snapshot{}, apperr.New("INVALID_CONFIG", "镜像目录不存在或不可读")
	}
	taskID := "deploy-" + randomID()
	if err := precheckResidualDirectories(cfg, targets); err != nil {
		return Snapshot{}, err
	}
	instanceIDs := make([]string, len(targets))
	for index, slot := range targets {
		instanceIDs[index] = fmt.Sprintf("%s_%d", cfg.ServerID, slot)
	}

	m.mu.RLock()
	activeID := m.active[serverID]
	m.mu.RUnlock()
	if activeID != "" {
		return Snapshot{}, apperr.New("DEPLOYMENT_ALREADY_RUNNING", "该镜像服务器组已有部署任务")
	}
	release, err := m.supervisor.ReserveDeployment(instanceIDs)
	if err != nil {
		return Snapshot{}, err
	}

	m.mu.Lock()
	if activeID := m.active[serverID]; activeID != "" {
		m.mu.Unlock()
		release()
		return Snapshot{}, apperr.New("DEPLOYMENT_ALREADY_RUNNING", "该镜像服务器组已有部署任务")
	}
	ctx, cancel := context.WithCancel(context.Background())
	item := &task{
		Snapshot: Snapshot{
			TaskID: taskID, ServerID: serverID, Targets: targets,
			Status: StatusQueued, CopyConcurrency: m.copyConcurrency,
			CreatedAt: time.Now().UTC(), Logs: make([]Log, 0, 64),
		},
		context: ctx, cancel: cancel, release: func() {
			release()
			imageLock.RUnlock()
		},
	}
	imageLocked = false
	m.tasks[taskID] = item
	m.active[serverID] = taskID
	m.appendLogLocked(item, "info", "queued", "", "部署任务已进入队列")
	snapshot := cloneSnapshot(item.Snapshot)
	m.mu.Unlock()
	go m.run(item, cfg)
	return snapshot, nil
}

func (m *Manager) WithImageMutation(serverID string, mutate func() error) error {
	lock := m.imageLock(serverID)
	if !lock.TryLock() {
		return apperr.New("INSTANCE_BUSY", "镜像源正在被部署任务使用")
	}
	defer lock.Unlock()
	return mutate()
}

func (m *Manager) imageLock(serverID string) *sync.RWMutex {
	value, _ := m.imageLocks.LoadOrStore(serverID, &sync.RWMutex{})
	return value.(*sync.RWMutex)
}

func (m *Manager) Get(taskID string) (Snapshot, error) {
	m.mu.RLock()
	item := m.tasks[taskID]
	if item == nil {
		m.mu.RUnlock()
		return Snapshot{}, apperr.New("DEPLOYMENT_NOT_FOUND", "部署任务不存在")
	}
	snapshot := cloneSnapshot(item.Snapshot)
	m.mu.RUnlock()
	return snapshot, nil
}

func (m *Manager) Active(serverID string) (Snapshot, error) {
	m.mu.RLock()
	taskID := m.active[serverID]
	item := m.tasks[taskID]
	if item == nil {
		m.mu.RUnlock()
		return Snapshot{}, apperr.New("DEPLOYMENT_NOT_FOUND", "该镜像服务器组当前没有部署任务")
	}
	snapshot := cloneSnapshot(item.Snapshot)
	m.mu.RUnlock()
	return snapshot, nil
}

func (m *Manager) Cancel(taskID string, force bool) (Snapshot, error) {
	m.mu.Lock()
	item := m.tasks[taskID]
	if item == nil {
		m.mu.Unlock()
		return Snapshot{}, apperr.New("DEPLOYMENT_NOT_FOUND", "部署任务不存在")
	}
	if isFinished(item.Status) {
		snapshot := cloneSnapshot(item.Snapshot)
		m.mu.Unlock()
		return snapshot, nil
	}
	item.force = force
	if force {
		item.Status = StatusForceStopRequested
		m.appendLogLocked(item, "warn", "force_stop_requested", item.CurrentInstance, "已请求强制结束部署")
	} else {
		item.Status = StatusCancelRequested
		m.appendLogLocked(item, "warn", "cancel_requested", item.CurrentInstance, "已请求在安全点取消部署")
	}
	item.cancel()
	snapshot := cloneSnapshot(item.Snapshot)
	m.mu.Unlock()
	return snapshot, nil
}

func (m *Manager) run(item *task, cfg model.ServerConfig) {
	defer item.release()
	now := time.Now().UTC()
	m.mu.Lock()
	item.Status = StatusRunning
	item.StartedAt = &now
	m.appendLogLocked(item, "info", "prechecking", "", "部署预检查完成")
	m.mu.Unlock()

	for _, slot := range item.Targets {
		if item.context.Err() != nil {
			m.finishCancelled(item)
			return
		}
		instanceID := fmt.Sprintf("%s_%d", cfg.ServerID, slot)
		m.mu.Lock()
		item.CurrentInstance = instanceID
		item.CopyStage = "preparing"
		item.CopyFilesTotal = 0
		item.CopyFilesDone = 0
		item.CopyBytesTotal = 0
		item.CopyBytesDone = 0
		m.appendLogLocked(item, "info", "stopping", instanceID, "正在准备目标实例")
		m.mu.Unlock()

		err := m.supervisor.DeployInstance(
			instanceID,
			func(target supervisor.DeploymentTarget) error {
				return m.deployFiles(item, cfg, target)
			},
			func() bool { return !m.isForce(item) },
			func() bool { return item.context.Err() != nil },
		)
		if err != nil {
			if item.context.Err() != nil {
				m.finishCancelled(item)
				return
			}
			m.mu.Lock()
			item.Failed++
			item.Error = err.Error()
			m.appendLogLocked(item, "error", "failed", instanceID, err.Error())
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			m.appendLogLocked(item, "info", "completed", instanceID, "目标实例部署完成")
			m.mu.Unlock()
		}
		m.mu.Lock()
		item.Completed++
		m.mu.Unlock()
	}

	finished := time.Now().UTC()
	m.mu.Lock()
	item.CurrentInstance = ""
	item.FinishedAt = &finished
	if item.Failed > 0 {
		item.Status = StatusCompletedWithErrors
		m.appendLogLocked(item, "warn", "completed", "", "部署完成，但部分实例失败")
	} else {
		item.Status = StatusCompleted
		item.Error = ""
		m.appendLogLocked(item, "info", "completed", "", "全部目标实例部署完成")
	}
	delete(m.active, item.ServerID)
	m.mu.Unlock()
}

func (m *Manager) finishCancelled(item *task) {
	finished := time.Now().UTC()
	m.mu.Lock()
	item.CurrentInstance = ""
	item.FinishedAt = &finished
	if item.force {
		item.Status = StatusForceStopped
		item.Error = "deployment force stopped"
		m.appendLogLocked(item, "warn", "force_stopped", "", "部署已强制结束，实例保持停止")
	} else {
		item.Status = StatusCancelled
		item.Error = "deployment cancelled"
		m.appendLogLocked(item, "warn", "cancelled", "", "部署已在安全点取消")
	}
	delete(m.active, item.ServerID)
	m.mu.Unlock()
}

func (m *Manager) isForce(item *task) bool {
	m.mu.RLock()
	force := item.force
	m.mu.RUnlock()
	return force
}

func (m *Manager) log(item *task, level, stage, instanceID, message string) {
	m.mu.Lock()
	m.appendLogLocked(item, level, stage, instanceID, message)
	m.mu.Unlock()
}

func (m *Manager) beginCopyProgress(item *task, stage string, files, bytes int64) {
	m.mu.Lock()
	item.CopyStage = stage
	item.CopyFilesTotal = files
	item.CopyFilesDone = 0
	item.CopyBytesTotal = bytes
	item.CopyBytesDone = 0
	m.mu.Unlock()
}

func (m *Manager) setCopyStage(item *task, stage string) {
	m.mu.Lock()
	item.CopyStage = stage
	m.mu.Unlock()
}

func (m *Manager) advanceCopyProgress(item *task, bytes int64, fileDone bool) {
	m.mu.Lock()
	item.CopyBytesDone += bytes
	if fileDone {
		item.CopyFilesDone++
	}
	m.mu.Unlock()
}

func (m *Manager) appendLogLocked(item *task, level, stage, instanceID, message string) {
	var sequence uint64 = 1
	if count := len(item.Logs); count > 0 {
		sequence = item.Logs[count-1].Sequence + 1
	}
	item.Logs = append(item.Logs, Log{
		Sequence: sequence, Timestamp: time.Now().UTC(), Level: level,
		Stage: stage, InstanceID: instanceID, Message: message,
	})
	if len(item.Logs) > 1000 {
		item.Logs = append([]Log(nil), item.Logs[len(item.Logs)-1000:]...)
	}
}

func normalizeTargets(requested []int, count int) ([]int, error) {
	if len(requested) == 0 {
		targets := make([]int, count)
		for index := range targets {
			targets[index] = index + 1
		}
		return targets, nil
	}
	unique := make(map[int]struct{}, len(requested))
	for _, slot := range requested {
		if slot < 1 || slot > count {
			return nil, apperr.New("INSTANCE_NOT_FOUND", "部署槽位超出当前实例数量")
		}
		unique[slot] = struct{}{}
	}
	targets := make([]int, 0, len(unique))
	for slot := range unique {
		targets = append(targets, slot)
	}
	sort.Ints(targets)
	return targets, nil
}

func precheckResidualDirectories(cfg model.ServerConfig, targets []int) error {
	for _, slot := range targets {
		instanceID := fmt.Sprintf("%s_%d", cfg.ServerID, slot)
		for _, prefix := range []string{".deploy-" + instanceID + "-", ".backup-" + instanceID + "-"} {
			matches, err := filepath.Glob(filepath.Join(cfg.RootPath, prefix+"*"))
			if err != nil {
				return apperr.Wrap("INVALID_CONFIG", "无法检查部署残留目录", err)
			}
			if len(matches) > 0 {
				return apperr.New("ROLLBACK_FAILED", "存在未处理的部署残留目录")
			}
		}
	}
	return nil
}

func isFinished(status Status) bool {
	switch status {
	case StatusCancelled, StatusForceStopped, StatusCompleted, StatusCompletedWithErrors, StatusFailed:
		return true
	default:
		return false
	}
}

func cloneSnapshot(source Snapshot) Snapshot {
	copy := source
	copy.Targets = append([]int(nil), source.Targets...)
	copy.Logs = append([]Log(nil), source.Logs...)
	return copy
}

func randomID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
