package supervisor

import (
	"io"
	"os/exec"
	"sync"
	"time"

	"PrismPanel-daemon/internal/model"
)

type State string

const (
	StateStopped   State = "stopped"
	StateStarting  State = "starting"
	StateRunning   State = "running"
	StateStopping  State = "stopping"
	StateDeploying State = "deploying"
	StateFailed    State = "failed"
)

type ConsoleLine struct {
	Type       string    `json:"type"`
	InstanceID string    `json:"instance_id"`
	SessionID  string    `json:"session_id"`
	Sequence   uint64    `json:"sequence"`
	Stream     string    `json:"stream"`
	Timestamp  time.Time `json:"timestamp"`
	Content    string    `json:"content"`
}

type Snapshot struct {
	InstanceID      string     `json:"instance_id"`
	ServerID        string     `json:"server_id"`
	ServerType      string     `json:"server_type"`
	Slot            int        `json:"slot,omitempty"`
	Name            string     `json:"name"`
	Workspace       string     `json:"workspace"`
	State           State      `json:"state"`
	PID             int        `json:"pid,omitempty"`
	SessionID       string     `json:"session_id,omitempty"`
	ConfiguredPort  int        `json:"configured_port"`
	FilePort        *int       `json:"file_port"`
	RuntimePort     *int       `json:"runtime_port"`
	PendingRestart  bool       `json:"pending_restart"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	ConsoleSequence uint64     `json:"console_sequence"`
}

type ring struct {
	capacity int
	lines    []ConsoleLine
}

func newRing(capacity int) *ring {
	return &ring{capacity: capacity, lines: make([]ConsoleLine, 0, capacity)}
}

func (r *ring) add(line ConsoleLine) {
	if len(r.lines) == r.capacity {
		copy(r.lines, r.lines[1:])
		r.lines[len(r.lines)-1] = line
		return
	}
	r.lines = append(r.lines, line)
}

func (r *ring) after(sequence uint64) []ConsoleLine {
	result := make([]ConsoleLine, 0, len(r.lines))
	for _, line := range r.lines {
		if line.Sequence > sequence {
			result = append(result, line)
		}
	}
	return result
}

type instance struct {
	op sync.Mutex
	mu sync.RWMutex

	cfg          model.InstanceConfig
	managed      bool
	state        State
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	done         chan struct{}
	expectedExit bool
	sessionID    string
	sequence     uint64
	console      *ring
	subscribers  map[uint64]chan ConsoleLine
	nextSubID    uint64
	pid          int
	runtimePort  *int
	startedAt    *time.Time
	lastError    string
	restarts     []time.Time
}

func newInstance(cfg model.InstanceConfig, consoleCapacity int) *instance {
	return &instance{
		cfg: cfg, managed: true, state: StateStopped, console: newRing(consoleCapacity),
		subscribers: make(map[uint64]chan ConsoleLine),
	}
}

func (i *instance) addConsole(stream, content string) ConsoleLine {
	i.mu.Lock()
	i.sequence++
	line := ConsoleLine{
		Type: "console.line", InstanceID: i.cfg.InstanceID, SessionID: i.sessionID,
		Sequence: i.sequence, Stream: stream, Timestamp: time.Now().UTC(), Content: content,
	}
	i.console.add(line)
	for _, subscriber := range i.subscribers {
		select {
		case subscriber <- line:
		default:
		}
	}
	i.mu.Unlock()
	return line
}

func (i *instance) snapshot() Snapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	filePort, _ := readServerPort(i.cfg.Workspace)
	return Snapshot{
		InstanceID: i.cfg.InstanceID, ServerID: i.cfg.ServerID, ServerType: i.cfg.ServerType,
		Slot: i.cfg.Slot, Name: i.cfg.Name, Workspace: i.cfg.Workspace, State: i.state,
		PID: i.pid, SessionID: i.sessionID, ConfiguredPort: i.cfg.Port, FilePort: filePort,
		RuntimePort: copyInt(i.runtimePort), PendingRestart: i.runtimePort != nil && *i.runtimePort != i.cfg.Port,
		StartedAt: i.startedAt, LastError: i.lastError, ConsoleSequence: i.sequence,
	}
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
