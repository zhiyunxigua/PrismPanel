package files

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/apperr"
)

func TestUploadChunkIsIdempotentAndPublishesOnFinalChunk(t *testing.T) {
	service, target, root := newTestService(t)
	remotePath := filepath.Join(root, "config.bin")
	base := []byte("base")
	payload := []byte("updated-payload")
	if err := os.WriteFile(remotePath, base, 0o640); err != nil {
		t.Fatal(err)
	}

	first := payload[:7]
	for attempt := 0; attempt < 2; attempt++ {
		_, next, err := service.UploadChunk(target, "config.bin", bytes.NewReader(first), int64(len(payload)), version(payload), true, version(base), "session-a", 0, false)
		if err != nil {
			t.Fatalf("upload first chunk attempt %d: %v", attempt, err)
		}
		if next != int64(len(first)) {
			t.Fatalf("unexpected next offset: %d", next)
		}
	}
	if current, err := os.ReadFile(remotePath); err != nil || !bytes.Equal(current, base) {
		t.Fatalf("remote file changed before finalize: %q, %v", current, err)
	}

	entry, next, err := service.UploadChunk(target, "config.bin", bytes.NewReader(payload[len(first):]), int64(len(payload)), version(payload), true, version(base), "session-a", int64(len(first)), true)
	if err != nil {
		t.Fatal(err)
	}
	if next != int64(len(payload)) || entry.Size != int64(len(payload)) {
		t.Fatalf("unexpected upload result: %#v, offset %d", entry, next)
	}
	retry, retryOffset, retryErr := service.UploadChunk(target, "config.bin", bytes.NewReader(payload[len(first):]), int64(len(payload)), version(payload), true, version(base), "session-a", int64(len(first)), true)
	if retryErr != nil || retryOffset != next || retry.Path != entry.Path || retry.Size != entry.Size {
		t.Fatalf("final chunk retry was not idempotent: %#v, %d, %v", retry, retryOffset, retryErr)
	}
	if current, err := os.ReadFile(remotePath); err != nil || !bytes.Equal(current, payload) {
		t.Fatalf("unexpected published contents: %q, %v", current, err)
	}
}

func TestUploadChunkRejectsRemoteVersionConflict(t *testing.T) {
	service, target, root := newTestService(t)
	remotePath := filepath.Join(root, "config.bin")
	base := []byte("base")
	payload := []byte("local-change")
	if err := os.WriteFile(remotePath, base, 0o640); err != nil {
		t.Fatal(err)
	}
	first := payload[:5]
	if _, _, err := service.UploadChunk(target, "config.bin", bytes.NewReader(first), int64(len(payload)), version(payload), true, version(base), "session-b", 0, false); err != nil {
		t.Fatal(err)
	}
	cloud := []byte("cloud-change")
	if err := os.WriteFile(remotePath, cloud, 0o640); err != nil {
		t.Fatal(err)
	}
	_, _, err := service.UploadChunk(target, "config.bin", bytes.NewReader(payload[len(first):]), int64(len(payload)), version(payload), true, version(base), "session-b", int64(len(first)), true)
	if apperr.From(err).Code != "FILE_CHANGED" {
		t.Fatalf("expected FILE_CHANGED, got %v", err)
	}
	if current, readErr := os.ReadFile(remotePath); readErr != nil || !bytes.Equal(current, cloud) {
		t.Fatalf("remote conflict was overwritten: %q, %v", current, readErr)
	}
}
