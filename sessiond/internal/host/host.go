package host

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"PrismPanel-daemon/sessionproto"
	"PrismPanel-sessiond/internal/procutil"
)

type SessionHost struct {
	mu          sync.Mutex
	instanceID  string
	sessionID   string
	token       string
	pluginToken string
	pid         int
	startedAt   time.Time
	stdin       io.WriteCloser
	sequence    uint64
	lines       []sessionproto.Frame
	subs        map[int]chan sessionproto.Frame
	nextSub     int
	clients     int
	lastSeen    time.Time
	activityCh  chan struct{}
	orphanCh    chan struct{}
	exited      bool
	exitCode    int
	exitErr     string
	done        chan struct{}
	closeOnce   sync.Once
	statePath   string
	socket      string
}

func Start(instanceID, workdir, command, socket, statePath, pluginToken string, env map[string]string, orphanTimeout time.Duration) (*SessionHost, error) {
	sessionID, err := randomID()
	if err != nil {
		return nil, err
	}
	token, err := randomID()
	if err != nil {
		return nil, err
	}
	if pluginToken == "" {
		pluginToken, err = randomID()
		if err != nil {
			return nil, err
		}
	}
	cmd, stdin, stdout, stderr, err := prepareCommand(workdir, command, mergeEnv(env, map[string]string{
		"PRISM_INSTANCE_ID":  instanceID,
		"PRISM_SESSION_ID":   sessionID,
		"PRISM_PLUGIN_TOKEN": pluginToken,
	}))
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	state := sessionproto.FreshState(instanceID, sessionID, socket, token, pluginToken, cmd.Process.Pid)
	if err := sessionproto.WriteState(statePath, state); err != nil {
		_ = procutil.KillProcessTreeByPID(cmd.Process.Pid)
		return nil, err
	}
	listener, err := sessionproto.Listen(socket)
	if err != nil {
		_ = procutil.KillProcessTreeByPID(cmd.Process.Pid)
		sessionproto.RemoveState(statePath)
		return nil, err
	}
	host := newHost(state, stdin, statePath)
	go host.scan("stdout", stdout)
	go host.scan("stderr", stderr)
	go func() { host.Finish(cmd.Wait()) }()
	startHost(host, listener, orphanTimeout)
	return host, nil
}

func AttachExisting(state sessionproto.State, statePath string, orphanTimeout time.Duration) (*SessionHost, error) {
	if state.PID <= 0 || !procutil.PidAlive(state.PID) {
		return nil, errors.New("existing session process is not running")
	}
	if state.Socket == "" {
		return nil, errors.New("existing session socket is empty")
	}
	listener, err := sessionproto.Listen(state.Socket)
	if err != nil {
		return nil, err
	}
	host := newHost(state, nil, statePath)
	go func() {
		host.Finish(procutil.WaitPID(state.PID))
	}()
	startHost(host, listener, orphanTimeout)
	return host, nil
}

func newHost(state sessionproto.State, stdin io.WriteCloser, statePath string) *SessionHost {
	return &SessionHost{
		instanceID:  state.InstanceID,
		sessionID:   state.SessionID,
		token:       state.Token,
		pluginToken: state.PluginToken,
		pid:         state.PID,
		startedAt:   state.StartedAt,
		stdin:       stdin,
		subs:        make(map[int]chan sessionproto.Frame),
		lastSeen:    time.Now(),
		activityCh:  make(chan struct{}, 1),
		orphanCh:    make(chan struct{}, 1),
		done:        make(chan struct{}),
		statePath:   statePath,
		socket:      state.Socket,
	}
}

func startHost(host *SessionHost, listener net.Listener, orphanTimeout time.Duration) {
	go host.serveListener(listener)
	go host.watchOrphan(time.NewTimer(orphanTimeout), orphanTimeout)
	go func() {
		select {
		case <-host.done:
		case <-host.orphanCh:
			_ = procutil.KillProcessTreeByPID(host.pid)
			<-host.done
		}
		_ = listener.Close()
		sessionproto.CleanupSocket(host.socket)
	}()
}

func (h *SessionHost) State() sessionproto.State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return sessionproto.State{InstanceID: h.instanceID, SessionID: h.sessionID, PID: h.pid, Socket: h.socket, Token: h.token, PluginToken: h.pluginToken, StartedAt: h.startedAt, Sequence: h.sequence}
}

func (h *SessionHost) Done() <-chan struct{} { return h.done }

func (h *SessionHost) Signal(name string) {
	_ = name
	_ = procutil.KillProcessTreeByPID(h.pid)
}

func (h *SessionHost) WriteStdin(content string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.exited || h.stdin == nil {
		return errors.New("session process is not running")
	}
	_, err := io.WriteString(h.stdin, content)
	return err
}

func (h *SessionHost) serveListener(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go h.Serve(conn)
	}
}

func (h *SessionHost) Serve(conn net.Conn) {
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
	if h.token != "" && hello.Token != h.token {
		_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeError, Error: "invalid session token"})
		return
	}
	h.mu.Lock()
	ready := sessionproto.Frame{Type: sessionproto.TypeHello, Instance: h.instanceID, Session: h.sessionID, PID: h.pid, Sequence: h.sequence, StartedAt: h.startedAt, Socket: h.socket, Token: h.token, PluginToken: h.pluginToken}
	h.mu.Unlock()
	if err := sessionproto.WriteFrame(conn, ready); err != nil {
		return
	}
	h.addClient()
	defer h.removeClient()
	output, cancel := h.subscribe(hello.After)
	defer cancel()
	read := make(chan struct {
		frame sessionproto.Frame
		err   error
	}, 1)
	go func() {
		for {
			frame, err := sessionproto.ReadFrame(reader)
			read <- struct {
				frame sessionproto.Frame
				err   error
			}{frame, err}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case frame, open := <-output:
			if !open {
				return
			}
			if err := sessionproto.WriteFrame(conn, frame); err != nil {
				return
			}
		case item := <-read:
			if item.err != nil {
				return
			}
			switch item.frame.Type {
			case sessionproto.TypeAttach:
				cancel()
				output, cancel = h.subscribe(item.frame.After)
			case sessionproto.TypeStdin:
				if err := h.WriteStdin(item.frame.Content); err != nil {
					_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeError, Error: err.Error()})
				}
			case sessionproto.TypeSignal:
				h.Signal(item.frame.Signal)
			case sessionproto.TypePing:
				_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypePong})
			default:
				_ = sessionproto.WriteFrame(conn, sessionproto.Frame{Type: sessionproto.TypeError, Error: "unsupported frame"})
			}
		}
	}
}

func (h *SessionHost) scan(stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		h.publishBytes(stream, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		h.publish("system", fmt.Sprintf("%s reader stopped: %v", stream, err))
	}
}

func (h *SessionHost) publish(stream, content string) {
	h.publishBytes(stream, []byte(content))
}

func (h *SessionHost) publishBytes(stream string, content []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sequence++
	frame := sessionproto.Frame{Type: streamType(stream), Instance: h.instanceID, Session: h.sessionID, PID: h.pid, Sequence: h.sequence, Stream: stream, ContentBytes: append([]byte(nil), content...)}
	h.lines = append(h.lines, frame)
	if len(h.lines) > 256 {
		h.lines = append([]sessionproto.Frame(nil), h.lines[len(h.lines)-256:]...)
	}
	for id, subscriber := range h.subs {
		select {
		case subscriber <- frame:
		default:
			delete(h.subs, id)
			close(subscriber)
		}
	}
}

func (h *SessionHost) subscribe(after uint64) (<-chan sessionproto.Frame, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextSub++
	id := h.nextSub
	channel := make(chan sessionproto.Frame, 256)
	for _, line := range h.lines {
		if line.Sequence > after {
			channel <- line
		}
	}
	if h.exited {
		channel <- h.exitFrameLocked()
	}
	h.subs[id] = channel
	return channel, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if existing, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(existing)
		}
	}
}

func (h *SessionHost) Finish(err error) {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.exited = true
		if err != nil {
			h.exitErr = err.Error()
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				h.exitCode = exitErr.ExitCode()
			} else {
				h.exitCode = 1
			}
		}
		frame := h.exitFrameLocked()
		for id, subscriber := range h.subs {
			select {
			case subscriber <- frame:
			default:
			}
			delete(h.subs, id)
			close(subscriber)
		}
		if h.stdin != nil {
			_ = h.stdin.Close()
		}
		h.mu.Unlock()
		sessionproto.RemoveState(h.statePath)
		close(h.done)
	})
}

func (h *SessionHost) exitFrameLocked() sessionproto.Frame {
	return sessionproto.Frame{Type: sessionproto.TypeExit, Instance: h.instanceID, Session: h.sessionID, PID: h.pid, Sequence: h.sequence, Code: h.exitCode, Error: h.exitErr}
}

func streamType(stream string) string {
	if stream == "stderr" {
		return sessionproto.TypeStderr
	}
	return sessionproto.TypeStdout
}

func prepareCommand(workdir, command string, extraEnv map[string]string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	cmd := commandFromLine(command)
	cmd.Dir = workdir
	cmd.Env = os.Environ()
	for key, value := range extraEnv {
		if key == "" {
			continue
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	procutil.ConfigureProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, nil, err
	}
	return cmd, stdin, stdout, stderr, nil
}

func mergeEnv(values ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, item := range values {
		for key, value := range item {
			if key == "" {
				continue
			}
			merged[key] = value
		}
	}
	return merged
}

func randomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func (h *SessionHost) addClient() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients++
	h.lastSeen = time.Now()
	h.notifyActivityLocked()
}

func (h *SessionHost) removeClient() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients > 0 {
		h.clients--
	}
	h.lastSeen = time.Now()
	h.notifyActivityLocked()
}

func (h *SessionHost) watchOrphan(timer *time.Timer, timeout time.Duration) {
	defer timer.Stop()
	for {
		select {
		case <-h.done:
			return
		case <-h.activityCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)
		case <-timer.C:
			h.mu.Lock()
			attached := h.clients > 0
			lastSeen := h.lastSeen
			h.mu.Unlock()
			if attached {
				timer.Reset(timeout)
				continue
			}
			remaining := timeout - time.Since(lastSeen)
			if remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			select {
			case h.orphanCh <- struct{}{}:
			default:
			}
			return
		}
	}
}

func (h *SessionHost) notifyActivityLocked() {
	select {
	case h.activityCh <- struct{}{}:
	default:
	}
}

func commandFromLine(command string) *exec.Cmd {
	fields, err := splitCommand(command)
	if err != nil || len(fields) == 0 {
		return procutil.ShellCommand(command)
	}
	return exec.Command(fields[0], fields[1:]...)
}

func splitCommand(command string) ([]string, error) {
	var (
		fields  []string
		current []rune
		inQuote bool
	)
	for _, item := range command {
		switch {
		case item == '"':
			inQuote = !inQuote
		case item == ' ' && !inQuote:
			if len(current) > 0 {
				fields = append(fields, string(current))
				current = current[:0]
			}
		default:
			current = append(current, item)
		}
	}
	if inQuote {
		return nil, errors.New("unterminated quote in command")
	}
	if len(current) > 0 {
		fields = append(fields, string(current))
	}
	return fields, nil
}
