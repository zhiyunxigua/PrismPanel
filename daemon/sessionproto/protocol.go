package sessionproto

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const ProtocolVersion = 2

const (
	TypeHello          = "hello"
	TypeAttach         = "attach"
	TypeStdin          = "stdin"
	TypeSignal         = "signal"
	TypeStdout         = "stdout"
	TypeStderr         = "stderr"
	TypeExit           = "exit"
	TypeError          = "error"
	TypePing           = "ping"
	TypePong           = "pong"
	TypeSessionList    = "session.list"
	TypeSessionStart   = "session.start"
	TypeSessionInspect = "session.inspect"
	TypeSessionStop    = "session.stop"
	TypeSessionKill    = "session.kill"
	TypeSessionResult  = "session.result"
)

type Frame struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	Success   *bool  `json:"success,omitempty"`
	Instance  string `json:"instance_id,omitempty"`
	Session   string `json:"session_id,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Sequence  uint64 `json:"sequence,omitempty"`
	After     uint64 `json:"after_sequence,omitempty"`
	Stream    string `json:"stream,omitempty"`
	Content   string `json:"content,omitempty"`
	// ContentBytes carries process output without converting it through UTF-8.
	ContentBytes     []byte            `json:"content_bytes,omitempty"`
	Signal           string            `json:"signal,omitempty"`
	Code             int               `json:"code,omitempty"`
	Error            string            `json:"error,omitempty"`
	Token            string            `json:"token,omitempty"`
	StartedAt        time.Time         `json:"started_at,omitempty"`
	Socket           string            `json:"socket,omitempty"`
	StatePath        string            `json:"state_path,omitempty"`
	Workdir          string            `json:"workdir,omitempty"`
	Command          string            `json:"command,omitempty"`
	PluginToken      string            `json:"plugin_token,omitempty"`
	OrphanTimeoutSec int               `json:"orphan_timeout_seconds,omitempty"`
	Sessions         []State           `json:"sessions,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
}

type State struct {
	InstanceID  string    `json:"instance_id"`
	SessionID   string    `json:"session_id"`
	PID         int       `json:"pid"`
	Socket      string    `json:"socket"`
	Token       string    `json:"token,omitempty"`
	PluginToken string    `json:"plugin_token,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	Sequence    uint64    `json:"sequence,omitempty"`
}

func WriteFrame(writer io.Writer, frame Frame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "%s\n", payload)
	return err
}

func ReadFrame(reader *bufio.Reader) (Frame, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return Frame{}, err
	}
	var frame Frame
	if err := json.Unmarshal(line, &frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}
