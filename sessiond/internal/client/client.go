package client

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"time"

	"PrismPanel-daemon/sessionproto"
)

type Manager struct {
	conn   net.Conn
	reader *bufio.Reader
}

func Dial(socket, token string, timeout time.Duration) (*Manager, error) {
	conn, err := sessionproto.Dial(socket, timeout)
	if err != nil {
		return nil, err
	}
	manager := &Manager{conn: conn, reader: bufio.NewReader(conn)}
	if err := sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeHello, Token: token}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	hello, err := sessionproto.ReadFrame(manager.reader)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if hello.Type == sessionproto.TypeError {
		_ = conn.Close()
		return nil, errors.New(hello.Error)
	}
	if hello.Type != sessionproto.TypeHello {
		_ = conn.Close()
		return nil, errors.New("invalid session manager handshake")
	}
	return manager, nil
}

func (m *Manager) Close() { _ = m.conn.Close() }

func (m *Manager) List() ([]sessionproto.State, error) {
	frame, err := m.roundTrip(sessionproto.Frame{Type: sessionproto.TypeSessionList, RequestID: "list"})
	if err != nil {
		return nil, err
	}
	return frame.Sessions, nil
}

func (m *Manager) Start(instanceID, workdir, command, pluginToken string, orphanTimeout int) (sessionproto.State, error) {
	frame, err := m.roundTrip(sessionproto.Frame{
		Type: sessionproto.TypeSessionStart, RequestID: "start", Instance: instanceID,
		Workdir: workdir, Command: command, PluginToken: pluginToken, OrphanTimeoutSec: orphanTimeout,
	})
	if err != nil {
		return sessionproto.State{}, err
	}
	return stateFrom(frame), nil
}

func (m *Manager) Inspect(instanceID string) (sessionproto.State, error) {
	frame, err := m.roundTrip(sessionproto.Frame{Type: sessionproto.TypeSessionInspect, RequestID: "inspect", Instance: instanceID})
	if err != nil {
		return sessionproto.State{}, err
	}
	return stateFrom(frame), nil
}

func (m *Manager) Stop(instanceID string) error {
	_, err := m.roundTrip(sessionproto.Frame{Type: sessionproto.TypeSessionStop, RequestID: "stop", Instance: instanceID})
	return err
}

func (m *Manager) Kill(instanceID string) error {
	_, err := m.roundTrip(sessionproto.Frame{Type: sessionproto.TypeSessionKill, RequestID: "kill", Instance: instanceID})
	return err
}

func (m *Manager) roundTrip(frame sessionproto.Frame) (sessionproto.Frame, error) {
	if err := sessionproto.WriteFrame(m.conn, frame); err != nil {
		return sessionproto.Frame{}, err
	}
	reply, err := sessionproto.ReadFrame(m.reader)
	if err != nil {
		return sessionproto.Frame{}, err
	}
	if reply.Type == sessionproto.TypeError || (reply.Success != nil && !*reply.Success) {
		if reply.Error == "" {
			reply.Error = "session manager request failed"
		}
		return sessionproto.Frame{}, errors.New(reply.Error)
	}
	return reply, nil
}

func stateFrom(frame sessionproto.Frame) sessionproto.State {
	return sessionproto.State{
		InstanceID: frame.Instance, SessionID: frame.Session, PID: frame.PID, Socket: frame.Socket,
		Token: frame.Token, PluginToken: frame.PluginToken, StartedAt: frame.StartedAt,
	}
}

func FormatState(state sessionproto.State) string {
	status := "running"
	if state.PID <= 0 {
		status = "unknown"
	}
	return fmt.Sprintf("%s\t%s\tpid %d", state.InstanceID, status, state.PID)
}
