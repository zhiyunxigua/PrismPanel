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

type PlayerSnapshot struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	Ping     int    `json:"ping"`
	JoinedAt string `json:"joined_at,omitempty"`
	ServerID string `json:"server_id,omitempty"`
}

type LoadedPlugin struct {
	ID         string   `json:"id,omitempty"`
	Name       string   `json:"name"`
	Version    string   `json:"version,omitempty"`
	Main       string   `json:"main,omitempty"`
	Authors    []string `json:"authors,omitempty"`
	Enabled    bool     `json:"enabled"`
	SourceFile string   `json:"source_file,omitempty"`
}

type PluginReport struct {
	TPS              *float64         `json:"tps,omitempty"`
	MSPT             *float64         `json:"mspt,omitempty"`
	OnlinePlayers    *int             `json:"online_players,omitempty"`
	MaxPlayers       *int             `json:"max_players,omitempty"`
	JVMHeapUsedBytes uint64           `json:"jvm_heap_used_bytes"`
	JVMHeapMaxBytes  uint64           `json:"jvm_heap_max_bytes"`
	JVMThreads       int              `json:"jvm_threads"`
	Players          []PlayerSnapshot `json:"players"`
	Plugins          []LoadedPlugin   `json:"plugins"`
}

type Snapshot struct {
	InstanceID             string              `json:"instance_id"`
	ServerID               string              `json:"server_id"`
	ServerType             string              `json:"server_type"`
	Platform               string              `json:"platform"`
	Slot                   int                 `json:"slot,omitempty"`
	Name                   string              `json:"name"`
	Workspace              string              `json:"workspace"`
	State                  State               `json:"state"`
	PID                    int                 `json:"pid,omitempty"`
	SessionID              string              `json:"session_id,omitempty"`
	ConfiguredPort         int                 `json:"configured_port"`
	FilePort               *int                `json:"file_port"`
	RuntimePort            *int                `json:"runtime_port"`
	PendingRestart         bool                `json:"pending_restart"`
	PluginPendingRestart   bool                `json:"plugin_pending_restart"`
	PluginOperationPending bool                `json:"-"`
	DeploymentLocked       bool                `json:"deployment_locked"`
	StartedAt              *time.Time          `json:"started_at,omitempty"`
	LastError              string              `json:"last_error,omitempty"`
	ConsoleSequence        uint64              `json:"console_sequence"`
	CPUPercent             *float64            `json:"cpu_percent,omitempty"`
	MemoryBytes            *uint64             `json:"memory_bytes,omitempty"`
	PluginConnected        bool                `json:"plugin_connected"`
	PluginCapabilities     []string            `json:"plugin_capabilities,omitempty"`
	ProxySync              *ProxySyncStatus    `json:"proxy_sync,omitempty"`
	OperatorSync           *OperatorSyncStatus `json:"operator_sync,omitempty"`
	PluginLastSeen         *time.Time          `json:"plugin_last_seen_at,omitempty"`
	TPS                    *float64            `json:"tps,omitempty"`
	MSPT                   *float64            `json:"mspt,omitempty"`
	OnlinePlayers          *int                `json:"online_players,omitempty"`
	MaxPlayers             *int                `json:"max_players,omitempty"`
	JVMHeapUsed            *uint64             `json:"jvm_heap_used_bytes,omitempty"`
	JVMHeapMax             *uint64             `json:"jvm_heap_max_bytes,omitempty"`
	JVMThreads             *int                `json:"jvm_threads,omitempty"`
	Players                []PlayerSnapshot    `json:"players,omitempty"`
	Plugins                []LoadedPlugin      `json:"plugins,omitempty"`
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

func (r *ring) clear() {
	r.lines = r.lines[:0]
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

	cfg                   model.InstanceConfig
	managed               bool
	state                 State
	cmd                   *exec.Cmd
	stdin                 io.WriteCloser
	done                  chan struct{}
	expectedExit          bool
	sessionID             string
	sequence              uint64
	console               *ring
	subscribers           map[uint64]chan ConsoleLine
	nextSubID             uint64
	pid                   int
	runtimePort           *int
	runtimeEncoding       string
	cpuPercent            *float64
	memoryBytes           *uint64
	startedAt             *time.Time
	lastError             string
	restarts              []time.Time
	pluginTokenHash       [32]byte
	pluginTokenSet        bool
	pluginGeneration      uint64
	pluginConnected       bool
	pluginLastSeen        *time.Time
	pluginReport          PluginReport
	pluginCapabilities    []string
	pluginConnection      *PluginConnection
	proxyCatalog          *ProxyBackendCatalog
	proxySync             ProxySyncStatus
	operatorSync          OperatorSyncStatus
	pluginPendingRestart  bool
	pluginRuntimeMismatch bool
	pluginFilesChanged    bool
	deploymentLocked      bool
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
	i.publishConsoleLocked(line)
	i.mu.Unlock()
	return line
}

func (i *instance) resetConsole(sessionID string) ConsoleLine {
	i.mu.Lock()
	i.sessionID = sessionID
	i.sequence++
	i.console.clear()
	line := ConsoleLine{
		Type: "console.reset", InstanceID: i.cfg.InstanceID, SessionID: sessionID,
		Sequence: i.sequence, Stream: "system", Timestamp: time.Now().UTC(),
	}
	i.publishConsoleLocked(line)
	i.mu.Unlock()
	return line
}

func (i *instance) publishConsoleLocked(line ConsoleLine) {
	i.console.add(line)
	for id, subscriber := range i.subscribers {
		select {
		case subscriber <- line:
		default:
			delete(i.subscribers, id)
			close(subscriber)
		}
	}
}

func (i *instance) snapshot() Snapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	filePort, _ := readServerPort(i.cfg.Workspace)
	pluginPendingRestart := i.pluginPendingRestart || i.pluginRuntimeMismatch ||
		(i.state == StateRunning && i.pluginFilesChanged)
	snapshot := Snapshot{
		InstanceID: i.cfg.InstanceID, ServerID: i.cfg.ServerID, ServerType: i.cfg.ServerType, Platform: i.cfg.Platform,
		Slot: i.cfg.Slot, Name: i.cfg.Name, Workspace: i.cfg.Workspace, State: i.state,
		PID: i.pid, SessionID: i.sessionID, ConfiguredPort: i.cfg.Port, FilePort: filePort,
		RuntimePort: copyInt(i.runtimePort), PendingRestart: pluginPendingRestart || i.runtimePort != nil &&
			(*i.runtimePort != i.cfg.Port || i.runtimeEncoding != i.cfg.Console.Encoding),
		PluginPendingRestart:   pluginPendingRestart,
		PluginOperationPending: i.pluginPendingRestart,
		DeploymentLocked:       i.deploymentLocked,
		StartedAt:              i.startedAt, LastError: i.lastError, ConsoleSequence: i.sequence,
		CPUPercent: copyFloat64(i.cpuPercent), MemoryBytes: copyUint64(i.memoryBytes),
		PluginConnected: i.pluginConnected, PluginCapabilities: append([]string(nil), i.pluginCapabilities...),
		PluginLastSeen: copyTime(i.pluginLastSeen), ProxySync: copyProxySync(i.proxySync),
		OperatorSync: copyOperatorSync(i.operatorSync),
	}
	if i.pluginConnected {
		snapshot.TPS = copyFloat64(i.pluginReport.TPS)
		snapshot.MSPT = copyFloat64(i.pluginReport.MSPT)
		snapshot.OnlinePlayers = copyInt(i.pluginReport.OnlinePlayers)
		snapshot.MaxPlayers = copyInt(i.pluginReport.MaxPlayers)
		snapshot.JVMHeapUsed = uint64Pointer(i.pluginReport.JVMHeapUsedBytes)
		snapshot.JVMHeapMax = uint64Pointer(i.pluginReport.JVMHeapMaxBytes)
		snapshot.JVMThreads = intPointer(i.pluginReport.JVMThreads)
		snapshot.Players = append([]PlayerSnapshot(nil), i.pluginReport.Players...)
		snapshot.Plugins = append([]LoadedPlugin(nil), i.pluginReport.Plugins...)
	}
	return snapshot
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyProxySync(value ProxySyncStatus) *ProxySyncStatus {
	if value.State == "" {
		return nil
	}
	copied := value
	return &copied
}

func copyOperatorSync(value OperatorSyncStatus) *OperatorSyncStatus {
	if value.State == "" {
		return nil
	}
	copied := value
	return &copied
}

func uint64Pointer(value uint64) *uint64 { return &value }
func intPointer(value int) *int          { return &value }
