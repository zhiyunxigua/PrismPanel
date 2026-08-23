package supervisor

import (
	"testing"

	"PrismPanel-daemon/internal/model"
)

func TestResetConsoleKeepsOnlyCurrentSessionHistory(t *testing.T) {
	current := newInstance(model.InstanceConfig{InstanceID: "test"}, 10)
	current.sessionID = "old-session"
	oldLine := current.addConsole("stdout", "old output")

	reset := current.resetConsole("new-session")
	newLine := current.addConsole("stdout", "new output")

	current.mu.RLock()
	history := current.console.after(0)
	current.mu.RUnlock()
	if len(history) != 2 {
		t.Fatalf("expected reset and new output, got %#v", history)
	}
	if history[0].Type != "console.reset" || history[0].SessionID != "new-session" {
		t.Fatalf("unexpected reset event: %#v", history[0])
	}
	if history[1].Content != "new output" || history[1].SessionID != "new-session" {
		t.Fatalf("unexpected current-session output: %#v", history[1])
	}
	if reset.Sequence <= oldLine.Sequence || newLine.Sequence <= reset.Sequence {
		t.Fatalf("console sequence must remain monotonic: old=%d reset=%d new=%d", oldLine.Sequence, reset.Sequence, newLine.Sequence)
	}
}

func TestResetConsoleNotifiesExistingSubscribers(t *testing.T) {
	current := newInstance(model.InstanceConfig{InstanceID: "test"}, 10)
	channel := make(chan ConsoleLine, 1)
	current.subscribers[1] = channel

	reset := current.resetConsole("new-session")
	received := <-channel
	if received.Type != "console.reset" || received.Sequence != reset.Sequence || received.SessionID != "new-session" {
		t.Fatalf("unexpected reset event: %#v", received)
	}
}

func TestSlowConsoleSubscriberIsClosedForReplay(t *testing.T) {
	current := newInstance(model.InstanceConfig{InstanceID: "test"}, 10)
	channel := make(chan ConsoleLine, 1)
	current.subscribers[1] = channel

	first := current.addConsole("stdout", "first")
	second := current.addConsole("stdout", "second")
	if received := <-channel; received.Sequence != first.Sequence {
		t.Fatalf("unexpected buffered line: %#v", received)
	}
	if _, open := <-channel; open {
		t.Fatal("expected slow subscriber channel to be closed")
	}
	if _, exists := current.subscribers[1]; exists {
		t.Fatal("expected slow subscriber to be removed")
	}

	current.mu.RLock()
	replay := current.console.after(first.Sequence)
	current.mu.RUnlock()
	if len(replay) != 1 || replay[0].Sequence != second.Sequence {
		t.Fatalf("unexpected replay history: %#v", replay)
	}
}

func TestSubscribeReplaysAfterSequenceReset(t *testing.T) {
	current := newInstance(model.InstanceConfig{InstanceID: "test"}, 10)
	current.addConsole("stdout", "current output")
	manager := &Manager{instances: map[string]*instance{"test": current}}

	history, _, cancel, err := manager.Subscribe("test", 100)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if len(history) != 1 || history[0].Content != "current output" {
		t.Fatalf("unexpected replay history: %#v", history)
	}
}
