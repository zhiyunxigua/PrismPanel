package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/sessionproto"
)

func (m *Manager) Start(instanceID string) error {
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	if !current.op.TryLock() {
		return apperr.New("INSTANCE_BUSY", "瀹炰緥姝ｅ湪鎵ц鍏朵粬鎿嶄綔")
	}
	defer current.op.Unlock()
	if err := deploymentLockError(current); err != nil {
		return err
	}
	return m.startLocked(current)
}

func (m *Manager) startLocked(current *instance) error {
	if m.closing.Load() {
		return apperr.New("INVALID_STATE", "瀹堟姢杩涚▼姝ｅ湪鍏抽棴")
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

	bound, err := m.ensureSession(current)
	if err != nil {
		m.markStartFailed(current, err)
		return apperr.Wrap("PROCESS_START_FAILED", "瀹炰緥鍚姩鍑嗗澶辫触", err)
	}
	now := time.Now().UTC()
	if bound.created {
		current.resetConsole(bound.sessionID)
	}
	current.mu.Lock()
	current.session = bound.client
	current.expectedExit = false
	current.sessionID = bound.sessionID
	current.pid = bound.client.hello.PID
	current.runtimePort = copyInt(&current.cfg.Port)
	current.runtimeEncoding = current.cfg.Console.Encoding
	runtimeEncoding := current.runtimeEncoding
	current.pluginTokenHash = sha256.Sum256([]byte(bound.pluginToken))
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
	current.addConsole("system", fmt.Sprintf("process started with pid %d", bound.client.hello.PID))
	m.publishState(current)

	go m.consumeSession(current, bound.client, runtimeEncoding)
	go m.sampleProcessMetrics(current, bound.client.hello.PID, bound.sessionID, bound.client.Done())
	go m.waitSession(current, bound.client)
	return nil
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

func (m *Manager) waitSession(current *instance, client *sessionConn) {
	err := client.Wait()
	current.mu.Lock()
	if current.session != client || client.Detached() {
		current.mu.Unlock()
		return
	}
	expected := current.expectedExit
	current.session = nil
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
		return apperr.New("INSTANCE_BUSY", "瀹炰緥姝ｅ湪鎵ц鍏朵粬鎿嶄綔")
	}
	defer current.op.Unlock()
	if err := deploymentLockError(current); err != nil {
		return err
	}
	return m.stopLocked(current)
}

func (m *Manager) stopLocked(current *instance) error {
	current.mu.Lock()
	if current.session == nil {
		current.state = StateStopped
		current.lastError = ""
		current.mu.Unlock()
		m.publishState(current)
		return nil
	}
	current.state = StateStopping
	current.expectedExit = true
	client := current.session
	done := client.Done()
	timeout := time.Duration(current.cfg.Process.StopTimeoutSeconds) * time.Second
	stopCommand := current.cfg.Process.StopCommand
	encoding := current.runtimeEncoding
	current.mu.Unlock()
	m.publishState(current)

	if client != nil {
		payload := stopCommand + "\n"
		encoded, encodeErr := encodeConsoleInput(encoding, payload)
		if encodeErr != nil {
			current.addConsole("system", fmt.Sprintf("stop command failed: %v", encodeErr))
		} else if err := client.writeStdin(encoded); err != nil {
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
		return apperr.New("INSTANCE_BUSY", "瀹炰緥姝ｅ湪鎵ц鍏朵粬鎿嶄綔")
	}
	defer current.op.Unlock()
	if err := deploymentLockError(current); err != nil {
		return err
	}
	current.mu.Lock()
	if current.session == nil {
		current.state = StateStopped
		current.lastError = ""
		current.mu.Unlock()
		m.publishState(current)
		return nil
	}
	current.state = StateStopping
	current.expectedExit = true
	done := current.session.Done()
	current.mu.Unlock()
	m.publishState(current)
	return m.forceKillAndWait(current, done)
}

func (m *Manager) forceKillAndWait(current *instance, done <-chan struct{}) error {
	current.mu.RLock()
	client := current.session
	pid := current.pid
	instanceID := current.cfg.InstanceID
	current.mu.RUnlock()
	if client != nil {
		_ = client.signal("kill")
	}
	_, _ = m.callSessionManager(sessionproto.Frame{Type: sessionproto.TypeSessionKill, RequestID: "kill-" + instanceID, Instance: instanceID})
	if pid > 0 {
		_ = KillProcessTreeByPID(pid)
	}
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return apperr.New("PROCESS_STOP_FAILED", "?????????????")
	}
}

func (m *Manager) Restart(instanceID string) error {
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	if !current.op.TryLock() {
		return apperr.New("INSTANCE_BUSY", "瀹炰緥姝ｅ湪鎵ц鍏朵粬鎿嶄綔")
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
		return apperr.New("INVALID_COMMAND", "鎺у埗鍙板懡浠ゅ寘鍚潪娉曞瓧绗︽垨瓒呰繃闀垮害闄愬埗")
	}
	current, err := m.lookup(instanceID)
	if err != nil {
		return err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.deploymentLocked {
		return apperr.New("INSTANCE_BUSY", "瀹炰緥宸茶閮ㄧ讲浠诲姟閿佸畾")
	}
	if current.state != StateRunning || current.session == nil {
		return apperr.New("INVALID_STATE", "实例当前未运行")
	}
	encoded, err := encodeConsoleInput(current.runtimeEncoding, command+"\n")
	if err != nil {
		return apperr.Wrap("PROCESS_INPUT_FAILED", "控制台命令写入失败", err)
	}
	if err := current.session.writeStdin(encoded); err != nil {
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
	_ = ctx
	m.DetachRunningSessions()
	return nil
}
