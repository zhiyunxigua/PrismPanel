package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
	"PrismPanel-daemon/sessionproto"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

type sessionBinding struct {
	client      *sessionConn
	sessionID   string
	pluginToken string
	created     bool
}

func (m *Manager) ensureSession(current *instance) (sessionBinding, error) {
	current.mu.RLock()
	cfg := current.cfg
	current.mu.RUnlock()
	if err := prepareWorkspace(cfg); err != nil {
		return sessionBinding{}, err
	}
	if existing, err := m.inspectSession(cfg.InstanceID); err == nil && existing.PID > 0 && existing.Socket != "" {
		client, err := m.attachExistingSession(cfg.InstanceID, existing.Socket, existing.Token)
		if err == nil {
			return sessionBinding{client: client, sessionID: client.hello.Session, pluginToken: firstNonEmpty(client.hello.PluginToken, existing.PluginToken), created: false}, nil
		}
	}
	pluginToken, err := randomPluginToken()
	if err != nil {
		return sessionBinding{}, err
	}
	client, err := m.startSession(cfg, pluginToken)
	if err != nil {
		return sessionBinding{}, err
	}
	return sessionBinding{client: client, sessionID: client.hello.Session, pluginToken: firstNonEmpty(client.hello.PluginToken, pluginToken), created: true}, nil
}

func (m *Manager) inspectSession(instanceID string) (sessionproto.State, error) {
	reply, err := m.callSessionManager(sessionproto.Frame{
		Type:      sessionproto.TypeSessionInspect,
		RequestID: "inspect-" + instanceID,
		Instance:  instanceID,
	})
	if err != nil {
		return sessionproto.State{}, err
	}
	return stateFromSessionResult(reply), nil
}

func (m *Manager) attachExistingSession(instanceID, socket, token string) (*sessionConn, error) {
	client, err := dialSession(socket, token, 2*time.Second)
	if err != nil {
		return nil, err
	}
	if instanceID != "" && client.hello.Instance != "" && client.hello.Instance != instanceID {
		client.close()
		return nil, errors.New("session instance mismatch")
	}
	return client, nil
}

func (m *Manager) startSession(cfg model.InstanceConfig, pluginToken string) (*sessionConn, error) {
	reply, err := m.callSessionManager(sessionproto.Frame{
		Type:             sessionproto.TypeSessionStart,
		RequestID:        "start-" + cfg.InstanceID,
		Instance:         cfg.InstanceID,
		Workdir:          cfg.Workspace,
		Command:          cfg.Process.StartCommand,
		PluginToken:      pluginToken,
		OrphanTimeoutSec: m.config.Process.SessionOrphanTimeoutSec,
		Env: map[string]string{
			"PRISM_DAEMON_WS":    m.pluginWebSocketURL(),
			"PRISM_INSTANCE_ID":  cfg.InstanceID,
			"PRISM_PLUGIN_TOKEN": pluginToken,
		},
	})
	if err != nil {
		return nil, err
	}
	state := stateFromSessionResult(reply)
	if state.Socket == "" || state.Token == "" {
		return nil, errors.New("session manager did not return attach coordinates")
	}
	client, err := m.attachExistingSession(cfg.InstanceID, state.Socket, state.Token)
	if err != nil {
		return nil, err
	}
	if state.SessionID != "" {
		client.hello.Session = state.SessionID
	}
	if state.PluginToken != "" {
		client.hello.PluginToken = state.PluginToken
	}
	return client, nil
}

func prepareWorkspace(cfg model.InstanceConfig) error {
	workspaceInfo, err := os.Stat(cfg.Workspace)
	if err != nil || !workspaceInfo.IsDir() {
		return errors.New("instance workspace is unavailable")
	}
	if _, err := filepath.EvalSymlinks(cfg.Workspace); err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if !model.IsProxyPlatform(cfg.Platform) {
		if err := writeServerPort(cfg.Workspace, cfg.Port); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) consumeSession(current *instance, client *sessionConn, encoding string) {
	reader := client.reader()
	for {
		frame, err := sessionproto.ReadFrame(reader)
		if err != nil {
			client.markExit(err)
			return
		}
		switch frame.Type {
		case sessionproto.TypeStdout, sessionproto.TypeStderr:
			stream := frame.Stream
			if stream == "" {
				if frame.Type == sessionproto.TypeStderr {
					stream = "stderr"
				} else {
					stream = "stdout"
				}
			}
			current.addConsole(stream, decodeSessionContent(encoding, frame.ContentBytes, frame.Content))
		case sessionproto.TypeExit:
			var exitErr error
			if frame.Error != "" {
				exitErr = errors.New(frame.Error)
			}
			client.markExit(exitErr)
			return
		case sessionproto.TypeError:
			current.addConsole("system", frame.Error)
		case sessionproto.TypePong:
		default:
		}
	}
}

func decodeSessionContent(encoding string, raw []byte, fallback string) string {
	if len(raw) > 0 {
		if strings.EqualFold(encoding, "gbk") {
			decoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), raw)
			if err == nil {
				return string(decoded)
			}
		}
		return string(raw)
	}
	return decodeSessionLine(encoding, fallback)
}

func decodeSessionLine(encoding, content string) string {
	if strings.EqualFold(encoding, "gbk") {
		decoded, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), content)
		if err == nil {
			return decoded
		}
	}
	return content
}

func encodeConsoleInput(encoding, content string) (string, error) {
	if strings.EqualFold(encoding, "gbk") {
		encoded, _, err := transform.String(simplifiedchinese.GBK.NewEncoder(), content)
		return encoded, err
	}
	return content, nil
}

func sessionStartError(err error) error {
	return apperr.Wrap("PROCESS_START_FAILED", "????????", err)
}

func stateFromSessionResult(frame sessionproto.Frame) sessionproto.State {
	if frame.Instance != "" || frame.Session != "" || frame.PID > 0 || frame.Socket != "" {
		return sessionproto.State{
			InstanceID:  frame.Instance,
			SessionID:   frame.Session,
			PID:         frame.PID,
			Socket:      frame.Socket,
			Token:       frame.Token,
			PluginToken: frame.PluginToken,
			StartedAt:   frame.StartedAt,
			Sequence:    frame.Sequence,
		}
	}
	if len(frame.Sessions) == 1 {
		return frame.Sessions[0]
	}
	return sessionproto.State{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
