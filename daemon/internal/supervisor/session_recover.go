package supervisor

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
	"time"

	"PrismPanel-daemon/sessionproto"
)

func (m *Manager) RecoverSessions() {
	reply, err := m.callSessionManager(sessionproto.Frame{Type: sessionproto.TypeSessionList, RequestID: "recover-list"})
	if err != nil {
		slog.Warn("skip session recovery", "error", err)
		return
	}
	for _, state := range reply.Sessions {
		if state.InstanceID == "" || state.Socket == "" {
			continue
		}
		current, err := m.lookup(state.InstanceID)
		if err != nil {
			continue
		}
		client, err := m.attachExistingSession(state.InstanceID, state.Socket, state.Token)
		if err != nil {
			slog.Warn("skip stale session", "instance", state.InstanceID, "error", err)
			continue
		}
		now := time.Now().UTC()
		if !client.hello.StartedAt.IsZero() {
			now = client.hello.StartedAt
		}
		pluginToken := firstNonEmpty(client.hello.PluginToken, state.PluginToken)
		current.mu.Lock()
		current.session = client
		current.expectedExit = false
		current.sessionID = client.hello.Session
		current.pid = client.hello.PID
		current.runtimePort = copyInt(&current.cfg.Port)
		current.runtimeEncoding = current.cfg.Console.Encoding
		runtimeEncoding := current.runtimeEncoding
		if pluginToken != "" {
			current.pluginTokenHash = sha256.Sum256([]byte(pluginToken))
			current.pluginTokenSet = true
		}
		current.pluginGeneration++
		current.pluginConnected = false
		current.pluginLastSeen = nil
		current.pluginReport = PluginReport{}
		current.pluginCapabilities = nil
		current.startedAt = &now
		current.state = StateRunning
		current.lastError = ""
		current.mu.Unlock()
		current.addConsole("system", fmt.Sprintf("reattached existing session pid %d", client.hello.PID))
		m.publishState(current)
		go m.consumeSession(current, client, runtimeEncoding)
		go m.sampleProcessMetrics(current, client.hello.PID, client.hello.Session, client.Done())
		go m.waitSession(current, client)
	}
}
