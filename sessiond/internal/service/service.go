package service

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"PrismPanel-daemon/sessionproto"
	"PrismPanel-sessiond/internal/config"
	"PrismPanel-sessiond/internal/host"
)

type Service struct {
	cfg      config.Config
	mu       sync.Mutex
	sessions map[string]*host.SessionHost
	listener net.Listener
}

func New(cfg config.Config) *Service {
	return &Service{cfg: cfg, sessions: map[string]*host.SessionHost{}}
}

func (s *Service) ListenAndServe() error {
	if err := os.MkdirAll(s.cfg.StateDir, 0o750); err != nil {
		return err
	}
	s.restore()
	listener, err := sessionproto.Listen(s.cfg.Listen)
	if err != nil {
		return err
	}
	s.listener = listener
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go s.serve(conn)
	}
}

func (s *Service) Close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
	sessionproto.CleanupSocket(s.cfg.Listen)
}

func (s *Service) List() []sessionproto.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]sessionproto.State, 0, len(s.sessions))
	for _, item := range s.sessions {
		items = append(items, item.State())
	}
	return items
}

func (s *Service) restore() {
	entries, err := os.ReadDir(s.cfg.StateDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		statePath := filepath.Join(s.cfg.StateDir, name)
		state, err := sessionproto.ReadState(statePath)
		if err != nil || state.InstanceID == "" {
			continue
		}
		item, err := host.AttachExisting(state, statePath, time.Duration(s.cfg.OrphanTimeoutSeconds)*time.Second)
		if err != nil {
			slog.Warn("skip stale session", "instance", state.InstanceID, "error", err)
			sessionproto.RemoveState(statePath)
			continue
		}
		s.mu.Lock()
		s.sessions[state.InstanceID] = item
		s.mu.Unlock()
		go s.watch(state.InstanceID, item)
	}
}

func (s *Service) serve(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	hello, err := sessionproto.ReadFrame(reader)
	if err != nil {
		return
	}
	if hello.Type != sessionproto.TypeHello {
		_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeError, Error: "first frame must be hello"})
		return
	}
	if hello.Token != s.cfg.Token {
		_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeError, Error: "invalid session manager token"})
		return
	}
	if err := sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeHello}); err != nil {
		return
	}
	for {
		frame, err := sessionproto.ReadFrame(reader)
		if err != nil {
			return
		}
		switch frame.Type {
		case sessionproto.TypeSessionList:
			_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeSessionResult, RequestID: frame.RequestID, Success: boolPtr(true), Sessions: s.List()})
		case sessionproto.TypeSessionInspect:
			state, err := s.inspect(frame.Instance)
			_ = writeResult(conn, frame.RequestID, state, err)
		case sessionproto.TypeSessionStart:
			state, err := s.start(frame)
			_ = writeResult(conn, frame.RequestID, state, err)
		case sessionproto.TypeSessionStop, sessionproto.TypeSessionKill:
			err := s.signal(frame.Instance, frame.Type == sessionproto.TypeSessionKill, frame.Content)
			_ = writeResult(conn, frame.RequestID, sessionproto.State{InstanceID: frame.Instance}, err)
		case sessionproto.TypePing:
			_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypePong, RequestID: frame.RequestID})
		default:
			_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeError, RequestID: frame.RequestID, Error: "unsupported manager frame"})
		}
	}
}

func (s *Service) start(frame sessionproto.Frame) (sessionproto.State, error) {
	if frame.Instance == "" || frame.Workdir == "" || frame.Command == "" {
		return sessionproto.State{}, errors.New("instance, workdir and command are required")
	}
	s.mu.Lock()
	if existing, ok := s.sessions[frame.Instance]; ok {
		state := existing.State()
		s.mu.Unlock()
		return state, nil
	}
	s.mu.Unlock()
	orphan := time.Duration(s.cfg.OrphanTimeoutSeconds) * time.Second
	if frame.OrphanTimeoutSec > 0 {
		orphan = time.Duration(frame.OrphanTimeoutSec) * time.Second
	}
	socket := sessionproto.SocketPath(s.cfg.StateDir, frame.Instance)
	statePath := sessionproto.StatePath(s.cfg.StateDir, frame.Instance)
	item, err := host.Start(frame.Instance, frame.Workdir, frame.Command, socket, statePath, frame.PluginToken, frame.Env, orphan)
	if err != nil {
		return sessionproto.State{}, err
	}
	s.mu.Lock()
	s.sessions[frame.Instance] = item
	s.mu.Unlock()
	go s.watch(frame.Instance, item)
	return item.State(), nil
}

func (s *Service) inspect(instanceID string) (sessionproto.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.sessions[instanceID]
	if !ok {
		return sessionproto.State{}, errors.New("session not found")
	}
	return item.State(), nil
}

func (s *Service) signal(instanceID string, kill bool, content string) error {
	s.mu.Lock()
	item, ok := s.sessions[instanceID]
	s.mu.Unlock()
	if !ok {
		return errors.New("session not found")
	}
	if kill {
		item.Signal("kill")
		return nil
	}
	if strings.TrimSpace(content) == "" {
		content = "stop\n"
	}
	return item.WriteStdin(content)
}

func (s *Service) watch(instanceID string, item *host.SessionHost) {
	<-item.Done()
	s.mu.Lock()
	delete(s.sessions, instanceID)
	s.mu.Unlock()
}

func writeResult(conn net.Conn, requestID string, state sessionproto.State, err error) error {
	frame := sessionproto.Frame{Type: sessionproto.TypeSessionResult, RequestID: requestID, Instance: state.InstanceID, Session: state.SessionID, PID: state.PID, Socket: state.Socket, Token: state.Token, PluginToken: state.PluginToken, StartedAt: state.StartedAt, Sequence: state.Sequence}
	if err != nil {
		frame.Error = err.Error()
		frame.Success = boolPtr(false)
	} else {
		frame.Success = boolPtr(true)
	}
	return sessionproto.WriteFrame(conn, frame)
}

func boolPtr(value bool) *bool { return &value }
