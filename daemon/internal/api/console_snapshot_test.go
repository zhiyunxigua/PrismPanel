package api

import (
	"strings"
	"testing"

	"PrismPanel-daemon/internal/supervisor"
)

func TestBuildConsoleSnapshotKeepsLatestBoundedLines(t *testing.T) {
	history := make([]supervisor.ConsoleLine, consoleSnapshotMaxLines+5)
	for index := range history {
		history[index] = supervisor.ConsoleLine{
			Type: "console.line", InstanceID: "test", SessionID: "session",
			Sequence: uint64(index + 1), Content: "line",
		}
	}
	snapshot := buildConsoleSnapshot(history, 0)
	if !snapshot.Truncated || len(snapshot.Lines) != consoleSnapshotMaxLines {
		t.Fatal("unexpected bounded snapshot")
	}
	if snapshot.FirstSequence != 6 || snapshot.Sequence != uint64(len(history)) {
		t.Fatal("unexpected snapshot sequence range")
	}
}

func TestBuildConsoleSnapshotKeepsLatestBoundedBytes(t *testing.T) {
	history := []supervisor.ConsoleLine{
		{Type: "console.line", Sequence: 1, Content: strings.Repeat("a", 300*1024)},
		{Type: "console.line", Sequence: 2, Content: strings.Repeat("b", 300*1024)},
	}
	snapshot := buildConsoleSnapshot(history, 0)
	if !snapshot.Truncated || len(snapshot.Lines) != 1 || snapshot.Lines[0].Sequence != 2 {
		t.Fatal("unexpected byte-bounded snapshot")
	}
}

func TestBuildConsoleSnapshotReportsSequenceGap(t *testing.T) {
	history := []supervisor.ConsoleLine{
		{Type: "console.line", Sequence: 10, Content: "latest"},
	}
	if snapshot := buildConsoleSnapshot(history, 5); !snapshot.Truncated {
		t.Fatal("expected snapshot to report a sequence gap")
	}
}
