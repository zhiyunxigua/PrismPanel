package supervisor

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
)

func (m *Manager) Start(instanceID string) error {
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	if !current.op.TryLock() {
		return apperr.New("INSTANCE_BUSY", "实例正在执行其他操作")
	}
	defer current.op.Unlock()
	if err := deploymentLockError(current); err != nil {
		return err
	}
	return m.startLocked(current)
}

func (m *Manager) startLocked(current *instance) error {
	if m.closing.Load() {
		return apperr.New("INVALID_STATE", "守护进程正在关闭")
	}
	current.mu.Lock()
	if current.state == StateRunning {
		current.mu.Unlock()
		return nil
	}
	if current.state == StateStarting || current.state == StateStopping {
		current.mu.Unlock()
		return apperr.New("INSTANCE_BUSY", "实例正在切换状态")
	}
	instanceID := current.cfg.InstanceID
	workspace := current.cfg.Workspace
	current.mu.Unlock()
	sessionID := randomSessionID()
	current.resetConsole(sessionID)
	m.beforeStartMu.RLock()
	beforeStart := m.beforeStart
	m.beforeStartMu.RUnlock()
	if beforeStart != nil {
		if err := beforeStart(instanceID, workspace); err != nil {
			m.markStartFailed(current, err)
			return apperr.Wrap("PLUGIN_OPERATION_FAILED", "pending plugin operation failed", err)
		}
	}
	current.mu.Lock()
	current.state = StateStarting
	current.lastError = ""
	current.mu.Unlock()
	m.publishState(current)

	pluginToken, err := randomPluginToken()
	if err != nil {
		m.markStartFailed(current, err)
		return apperr.Wrap("PROCESS_START_FAILED", "无法生成插件实例凭据", err)
	}
	cmd, stdin, stdout, stderr, err := m.prepareCommand(current, sessionID, pluginToken)
	if err != nil {
		m.markStartFailed(current, err)
		return apperr.Wrap("PROCESS_START_FAILED", "实例启动准备失败", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		m.markStartFailed(current, err)
		return apperr.Wrap("PROCESS_START_FAILED", "无法创建实例进程", err)
	}
	now := time.Now().UTC()
	done := make(chan struct{})
	current.mu.Lock()
	current.cmd = cmd
	current.stdin = stdin
	current.done = done
	current.expectedExit = false
	current.sessionID = sessionID
	current.pid = cmd.Process.Pid
	current.runtimePort = copyInt(&current.cfg.Port)
	current.runtimeEncoding = current.cfg.Console.Encoding
	runtimeEncoding := current.runtimeEncoding
	current.pluginTokenHash = sha256.Sum256([]byte(pluginToken))
	current.pluginTokenSet = true
	current.pluginGeneration++
	current.pluginConnected = false
	current.pluginLastSeen = nil
	current.pluginReport = PluginReport{}
	current.pluginCapabilities = nil
	current.pluginPendingRestart = false
	current.pluginRuntimeMismatch = false
	current.pluginFilesChanged = false
	current.cpuPercent = nil
	current.memoryBytes = nil
	current.startedAt = &now
	current.state = StateRunning
	current.mu.Unlock()
	current.addConsole("system", fmt.Sprintf("process started with pid %d", cmd.Process.Pid))
	m.publishState(current)

	go m.scanOutput(current, "stdout", stdout, runtimeEncoding)
	go m.scanOutput(current, "stderr", stderr, runtimeEncoding)
	go m.sampleProcessMetrics(current, cmd.Process.Pid, sessionID, done)
	go m.waitProcess(current, cmd, done)
	return nil
}

func (m *Manager) prepareCommand(
	current *instance,
	sessionID string,
	pluginToken string,
) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	current.mu.RLock()
	cfg := current.cfg
	current.mu.RUnlock()
	workspaceInfo, err := os.Stat(cfg.Workspace)
	if err != nil || !workspaceInfo.IsDir() {
		return nil, nil, nil, nil, errors.New("instance workspace is unavailable")
	}
	if _, err := filepath.EvalSymlinks(cfg.Workspace); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("resolve workspace: %w", err)
	}
	if !model.IsProxyPlatform(cfg.Platform) {
		if err := writeServerPort(cfg.Workspace, cfg.Port); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	cmd := shellCommand(cfg.Process.StartCommand)
	cmd.Dir = cfg.Workspace
	cmd.Env = append(os.Environ(),
		"PRISM_DAEMON_WS="+m.pluginWebSocketURL(),
		"PRISM_INSTANCE_ID="+cfg.InstanceID,
		"PRISM_SESSION_ID="+sessionID,
		"PRISM_PLUGIN_TOKEN="+pluginToken,
	)
	configureProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, nil, nil, nil, err
	}
	return cmd, stdin, stdout, stderr, nil
}

func randomSessionID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func randomPluginToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func (m *Manager) pluginWebSocketURL() string {
	scheme := "ws"
	if m.config.SSL.Enabled {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://127.0.0.1:%d/api/v1/ws/plugin", scheme, m.config.Server.Port)
}

func (m *Manager) scanOutput(current *instance, stream string, reader io.Reader, encoding string) {
	scanner := bufio.NewScanner(decodeConsoleOutput(encoding, reader))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		current.addConsole(stream, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		current.addConsole("system", fmt.Sprintf("%s reader stopped: %v", stream, err))
	}
}

func (m *Manager) waitProcess(current *instance, cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	current.mu.Lock()
	if current.cmd != cmd {
		current.mu.Unlock()
		return
	}
	expected := current.expectedExit
	current.cmd = nil
	current.stdin = nil
	current.pid = 0
	current.runtimePort = nil
	current.runtimeEncoding = ""
	current.cpuPercent = nil
	current.memoryBytes = nil
	current.pluginTokenHash = [32]byte{}
	current.pluginTokenSet = false
	pluginConnection := current.pluginConnection
	current.pluginConnection = nil
	current.pluginGeneration++
	current.pluginConnected = false
	current.pluginReport = PluginReport{}
	current.pluginCapabilities = nil
	if expected {
		current.state = StateStopped
		current.lastError = ""
	} else {
		current.state = StateFailed
		if err != nil {
			current.lastError = err.Error()
		} else {
			current.lastError = "process exited unexpectedly"
		}
	}
	shouldRestart := !expected && current.managed && current.cfg.Process.AutoRestart && m.allowAutoRestartLocked(current)
	current.mu.Unlock()
	if pluginConnection != nil {
		pluginConnection.Close()
	}
	if err != nil {
		current.addConsole("system", fmt.Sprintf("process exited: %v", err))
	} else {
		current.addConsole("system", "process exited")
	}
	m.publishState(current)
	close(done)
	if shouldRestart {
		instanceID := current.snapshot().InstanceID
		time.AfterFunc(2*time.Second, func() {
			if err := m.Start(instanceID); err != nil {
				current.addConsole("system", fmt.Sprintf("automatic restart failed: %v", err))
			}
		})
	}
}

func (m *Manager) allowAutoRestartLocked(current *instance) bool {
	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)
	filtered := current.restarts[:0]
	for _, item := range current.restarts {
		if item.After(cutoff) {
			filtered = append(filtered, item)
		}
	}
	current.restarts = filtered
	if len(current.restarts) >= 3 {
		current.lastError = "automatic restart limit reached"
		return false
	}
	current.restarts = append(current.restarts, now)
	return true
}

func (m *Manager) markStartFailed(current *instance, err error) {
	current.mu.Lock()
	current.state = StateFailed
	current.lastError = err.Error()
	current.mu.Unlock()
	current.addConsole("system", fmt.Sprintf("process start failed: %v", err))
	m.publishState(current)
}

func (m *Manager) Stop(instanceID string) error {
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	if !current.op.TryLock() {
		return apperr.New("INSTANCE_BUSY", "实例正在执行其他操作")
	}
	defer current.op.Unlock()
	if err := deploymentLockError(current); err != nil {
		return err
	}
	return m.stopLocked(current)
}

func (m *Manager) stopLocked(current *instance) error {
	current.mu.Lock()
	if current.cmd == nil {
		current.state = StateStopped
		current.lastError = ""
		current.mu.Unlock()
		m.publishState(current)
		return nil
	}
	current.state = StateStopping
	current.expectedExit = true
	stdin := current.stdin
	done := current.done
	timeout := time.Duration(current.cfg.Process.StopTimeoutSeconds) * time.Second
	stopCommand := current.cfg.Process.StopCommand
	encoding := current.runtimeEncoding
	current.mu.Unlock()
	m.publishState(current)

	if stdin != nil {
		if err := writeConsoleInput(stdin, encoding, stopCommand+"\n"); err != nil {
			current.addConsole("system", fmt.Sprintf("stop command failed: %v", err))
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		current.addConsole("system", "stop timeout reached, killing process tree")
		return m.forceKillAndWait(current, done)
	}
}

func (m *Manager) Kill(instanceID string) error {
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	if !current.op.TryLock() {
		return apperr.New("INSTANCE_BUSY", "实例正在执行其他操作")
	}
	defer current.op.Unlock()
	if err := deploymentLockError(current); err != nil {
		return err
	}
	current.mu.Lock()
	if current.cmd == nil {
		current.state = StateStopped
		current.lastError = ""
		current.mu.Unlock()
		m.publishState(current)
		return nil
	}
	current.state = StateStopping
	current.expectedExit = true
	done := current.done
	current.mu.Unlock()
	m.publishState(current)
	return m.forceKillAndWait(current, done)
}

func (m *Manager) forceKillAndWait(current *instance, done <-chan struct{}) error {
	current.mu.RLock()
	cmd := current.cmd
	current.mu.RUnlock()
	if cmd != nil {
		if err := killProcessTree(cmd); err != nil {
			return apperr.Wrap("PROCESS_STOP_FAILED", "无法强制结束实例进程组", err)
		}
	}
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return apperr.New("PROCESS_STOP_FAILED", "强制结束后无法确认进程退出")
	}
}

func (m *Manager) Restart(instanceID string) error {
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	if !current.op.TryLock() {
		return apperr.New("INSTANCE_BUSY", "实例正在执行其他操作")
	}
	defer current.op.Unlock()
	if err := deploymentLockError(current); err != nil {
		return err
	}
	if err := m.stopLocked(current); err != nil {
		return err
	}
	return m.startLocked(current)
}

func (m *Manager) Command(instanceID, command string) error {
	if command == "" {
		return apperr.New("INVALID_COMMAND", "控制台命令不能为空")
	}
	if len(command) > 8192 || strings.ContainsAny(command, "\x00\r\n") {
		return apperr.New("INVALID_COMMAND", "控制台命令包含非法字符或超过长度限制")
	}
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.deploymentLocked {
		return apperr.New("INSTANCE_BUSY", "实例已被部署任务锁定")
	}
	if current.state != StateRunning || current.stdin == nil {
		return apperr.New("INVALID_STATE", "实例当前未运行")
	}
	if err := writeConsoleInput(current.stdin, current.runtimeEncoding, command+"\n"); err != nil {
		return apperr.Wrap("PROCESS_INPUT_FAILED", "控制台命令写入失败", err)
	}
	return nil
}

func (m *Manager) StartAuto() {
	m.mu.RLock()
	ids := make([]string, 0)
	for id, current := range m.instances {
		current.mu.RLock()
		autoStart := current.cfg.Process.AutoStart
		current.mu.RUnlock()
		if autoStart {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	sort.Strings(ids)
	for _, id := range ids {
		if err := m.Start(id); err != nil {
			if current, lookupErr := m.lookup(id); lookupErr == nil {
				current.addConsole("system", fmt.Sprintf("automatic start failed: %v", err))
			}
		}
	}
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.closing.Store(true)
	m.mu.RLock()
	instances := make([]*instance, 0, len(m.instances))
	for _, current := range m.instances {
		instances = append(instances, current)
	}
	m.mu.RUnlock()
	sort.Slice(instances, func(i, j int) bool {
		instances[i].mu.RLock()
		left := instances[i].cfg.InstanceID
		instances[i].mu.RUnlock()
		instances[j].mu.RLock()
		right := instances[j].cfg.InstanceID
		instances[j].mu.RUnlock()
		return left < right
	})
	for _, current := range instances {
		current.mu.RLock()
		state := current.state
		current.mu.RUnlock()
		if state != StateRunning && state != StateFailed {
			continue
		}
		done := make(chan error, 1)
		go func(item *instance) {
			item.op.Lock()
			defer item.op.Unlock()
			done <- m.stopLocked(item)
		}(current)
		select {
		case <-ctx.Done():
			for _, remaining := range instances {
				remaining.mu.Lock()
				remaining.expectedExit = true
				cmd := remaining.cmd
				remaining.mu.Unlock()
				if cmd != nil {
					_ = killProcessTree(cmd)
				}
			}
			return ctx.Err()
		case <-done:
		}
	}
	return nil
}
