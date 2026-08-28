package supervisor

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"PrismPanel-daemon/sessionproto"
)

func (m *Manager) sessionToken() (string, error) {
	contents, err := os.ReadFile(m.config.SessionTokenFile())
	if err != nil {
		return "", fmt.Errorf("read session token: %w", err)
	}
	token := strings.TrimSpace(string(contents))
	if token == "" {
		return "", errors.New("session token is empty")
	}
	return token, nil
}

func (m *Manager) callSessionManager(frame sessionproto.Frame) (sessionproto.Frame, error) {
	token, err := m.sessionToken()
	if err != nil {
		return sessionproto.Frame{}, err
	}
	conn, err := sessionproto.Dial(m.config.SessionSocket(), 2*time.Second)
	if err != nil {
		return sessionproto.Frame{}, fmt.Errorf("connect prism-sessiond: %w", err)
	}
	defer conn.Close()
	if err := sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeHello, Token: token}); err != nil {
		return sessionproto.Frame{}, err
	}
	reader := bufio.NewReader(conn)
	hello, err := sessionproto.ReadFrame(reader)
	if err != nil {
		return sessionproto.Frame{}, err
	}
	if hello.Type == sessionproto.TypeError {
		return sessionproto.Frame{}, errors.New(hello.Error)
	}
	if hello.Type != sessionproto.TypeHello {
		return sessionproto.Frame{}, errors.New("invalid session manager handshake")
	}
	if err := sessionproto.WriteFrame(conn, frame); err != nil {
		return sessionproto.Frame{}, err
	}
	reply, err := sessionproto.ReadFrame(reader)
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
