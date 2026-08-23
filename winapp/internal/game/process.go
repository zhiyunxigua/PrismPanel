package game

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var ErrGameAlreadyRunning = errors.New("game is already running")

type ProcessStartRequest struct {
	JavaPath string
	Args     []string
	WorkDir  string
	LogPath  string
	Env      []string
	Cleanup  func()
}

type GameProcess struct {
	ServerID  string    `json:"server_id"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	LogPath   string    `json:"log_path"`
	cmd       *exec.Cmd
}

type ProcessManager struct {
	mu        sync.Mutex
	processes map[string]*GameProcess
}

func NewProcessManager() *ProcessManager {
	return &ProcessManager{processes: make(map[string]*GameProcess)}
}

func (m *ProcessManager) Running(serverID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	process := m.processes[serverID]
	if process == nil {
		return false
	}
	if process.cmd == nil || process.cmd.ProcessState != nil {
		delete(m.processes, serverID)
		return false
	}
	return true
}

func (m *ProcessManager) AnyRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for serverID, process := range m.processes {
		if process == nil || process.cmd == nil || process.cmd.ProcessState != nil {
			delete(m.processes, serverID)
			continue
		}
		return true
	}
	return false
}

// Close 强制结束指定游戏进程并等待退出。
func (m *ProcessManager) Close(serverID string) error {
	m.mu.Lock()
	process := m.processes[serverID]
	m.mu.Unlock()
	if process == nil || process.cmd == nil || process.cmd.ProcessState != nil {
		return nil
	}
	err := process.cmd.Process.Kill()
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	_ = process.cmd.Wait()
	return nil
}

func (m *ProcessManager) Start(serverID string, request ProcessStartRequest) (GameProcess, error) {
	m.mu.Lock()
	if process := m.processes[serverID]; process != nil && process.cmd != nil && process.cmd.ProcessState == nil {
		m.mu.Unlock()
		return GameProcess{}, ErrGameAlreadyRunning
	}
	delete(m.processes, serverID)
	m.mu.Unlock()

	if request.JavaPath == "" {
		return GameProcess{}, errors.New("java path is required")
	}
	if request.WorkDir == "" {
		return GameProcess{}, errors.New("game working directory is required")
	}
	if err := os.MkdirAll(request.WorkDir, 0o755); err != nil {
		return GameProcess{}, err
	}
	logFile, err := os.OpenFile(request.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return GameProcess{}, err
	}
	cmd := exec.Command(request.JavaPath, request.Args...)
	hideProcessWindow(cmd)
	cmd.Dir = request.WorkDir
	if len(request.Env) > 0 {
		cmd.Env = append(os.Environ(), request.Env...)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		if request.Cleanup != nil {
			request.Cleanup()
		}
		return GameProcess{}, err
	}
	process := &GameProcess{ServerID: serverID, PID: cmd.Process.Pid, StartedAt: time.Now().UTC(), LogPath: request.LogPath, cmd: cmd}
	m.mu.Lock()
	m.processes[serverID] = process
	m.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		// 记录退出码（参照 PCL2 Watcher 的退出判定）：非 0 表示异常退出，便于排查
		exitCode := 0
		if waitErr != nil {
			if exitError, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				exitCode = -1
			}
		}
		_, _ = fmt.Fprintf(logFile, "\n[PrismPanel] 游戏进程已退出，退出码 %d\n", exitCode)
		_ = logFile.Close()
		if request.Cleanup != nil {
			request.Cleanup()
		}
		m.mu.Lock()
		if current := m.processes[serverID]; current == process {
			delete(m.processes, serverID)
		}
		m.mu.Unlock()
	}()
	return *process, nil
}

func DefaultGameLogPath(server ServerConfig) (string, error) {
	paths, err := DefaultCachePathsForVersion(server.VersionLabel)
	if err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("%s-%s.log", safePathSegment(server.ID), stamp)
	return filepath.Join(paths.Root, "logs", name), nil
}
