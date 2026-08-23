package api

import "PrismPanel-daemon/internal/supervisor"

const (
	consoleSnapshotMaxLines = 1000
	consoleSnapshotMaxBytes = 512 * 1024
)

type consoleSnapshot struct {
	Type          string                   `json:"type"`
	InstanceID    string                   `json:"instance_id"`
	SessionID     string                   `json:"session_id"`
	FirstSequence uint64                   `json:"first_sequence"`
	Sequence      uint64                   `json:"sequence"`
	Truncated     bool                     `json:"truncated"`
	Lines         []supervisor.ConsoleLine `json:"lines"`
}

func buildConsoleSnapshot(history []supervisor.ConsoleLine, afterSequence uint64) consoleSnapshot {
	result := consoleSnapshot{Type: "console.snapshot"}
	if len(history) == 0 {
		return result
	}

	start := len(history)
	bytes := 0
	for start > 0 && len(history)-start < consoleSnapshotMaxLines {
		lineBytes := len(history[start-1].Content) + 1
		if bytes > 0 && bytes+lineBytes > consoleSnapshotMaxBytes {
			break
		}
		bytes += lineBytes
		start--
	}

	last := history[len(history)-1]
	result.InstanceID = last.InstanceID
	result.SessionID = last.SessionID
	result.Sequence = last.Sequence
	result.Lines = append([]supervisor.ConsoleLine(nil), history[start:]...)
	result.FirstSequence = result.Lines[0].Sequence
	result.Truncated = start > 0 || afterSequence > 0 && history[0].Sequence > afterSequence+1
	return result
}
