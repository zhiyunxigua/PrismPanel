package sessionproto

import (
	"bufio"
	"bytes"
	"path/filepath"
	"testing"
)

func TestWriteReadFrameRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, Frame{Type: TypeHello, Instance: "bedwars_1", Session: "abc", PID: 12}); err != nil {
		t.Fatal(err)
	}
	frame, err := ReadFrame(bufio.NewReader(&buffer))
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != TypeHello || frame.Instance != "bedwars_1" || frame.PID != 12 {
		t.Fatalf("frame = %#v", frame)
	}
}

func TestWriteReadFramePreservesRawContentBytes(t *testing.T) {
	var buffer bytes.Buffer
	raw := []byte{0xb7, 0xfe, 0xce, 0xf1}
	if err := WriteFrame(&buffer, Frame{Type: TypeStdout, ContentBytes: raw}); err != nil {
		t.Fatal(err)
	}
	frame, err := ReadFrame(bufio.NewReader(&buffer))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.ContentBytes, raw) {
		t.Fatalf("content bytes = %x, want %x", frame.ContentBytes, raw)
	}
}

func TestSessionPaths(t *testing.T) {
	socket := SocketPath("data", "bedwars_1")
	state := StatePath("data", "bedwars_1")
	if filepath.Base(socket) != "bedwars_1.sock" || filepath.Base(state) != "bedwars_1.json" {
		t.Fatalf("socket=%s state=%s", socket, state)
	}
}
