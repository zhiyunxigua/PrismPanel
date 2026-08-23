package secret

import (
	"path/filepath"
	"testing"
)

func TestResetPreservesNodeID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	initial, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created || initial.NodeID == "" || initial.Secret == "" {
		t.Fatalf("unexpected initial secret: %#v", initial)
	}
	reset, err := Reset(path)
	if err != nil {
		t.Fatal(err)
	}
	if reset.NodeID != initial.NodeID {
		t.Fatalf("node id changed: %s != %s", reset.NodeID, initial.NodeID)
	}
	if reset.Secret == initial.Secret {
		t.Fatal("secret was not rotated")
	}
}
