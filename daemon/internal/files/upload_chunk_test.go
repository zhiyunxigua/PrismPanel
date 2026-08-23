package files

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/apperr"
)

func TestUploadSessionStatusSurvivesInMemoryStateLossAndCancel(t *testing.T) {
	service, target, root := newTestService(t)
	payload := []byte("resume-this-upload")
	first := payload[:7]
	if _, _, err := service.UploadChunk(target, "resume.bin", bytes.NewReader(first), int64(len(payload)), version(payload), false, "", "resume-session", 0, false); err != nil {
		t.Fatal(err)
	}

	service.uploadMu.Lock()
	state := service.uploads["resume-session"]
	delete(service.uploads, "resume-session")
	service.uploadMu.Unlock()
	if state != nil && state.timer != nil {
		state.timer.Stop()
	}

	status, err := service.UploadSessionStatus(target, "resume.bin", "resume-session")
	if err != nil || status.Offset != int64(len(first)) || status.Complete {
		t.Fatalf("unexpected recovered status: %#v, %v", status, err)
	}
	if _, err := service.CancelUpload(target, "resume.bin", "resume-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".prism-upload-resume-session")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("upload temporary file was not removed: %v", err)
	}
}

func TestUploadChunkKeepsCompletedTemporaryFileAfterPublishConflict(t *testing.T) {
	service, target, _ := newTestService(t)
	if err := service.Create(target, "conflict.bin", "file"); err != nil {
		t.Fatal(err)
	}
	payload := []byte("replacement")
	if _, _, err := service.UploadChunk(target, "conflict.bin", bytes.NewReader(payload), int64(len(payload)), version(payload), false, "", "conflict-session", 0, true); err == nil {
		t.Fatal("expected publish conflict")
	}
	status, err := service.UploadSessionStatus(target, "conflict.bin", "conflict-session")
	if err != nil || status.Offset != int64(len(payload)) || status.Complete {
		t.Fatalf("completed temporary upload was not retained: %#v, %v", status, err)
	}
}

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

func TestUploadChunkCreatesMissingParentDirectories(t *testing.T) {
	service, target, root := newTestService(t)
	payload := []byte("chunked")
	entry, next, err := service.UploadChunk(
		target, "folder/nested/file.bin", bytes.NewReader(payload), int64(len(payload)), version(payload),
		false, "", "nested-session", 0, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if next != int64(len(payload)) || entry.Path != "folder/nested/file.bin" {
		t.Fatalf("unexpected upload result: %#v, offset %d", entry, next)
	}
	contents, err := os.ReadFile(filepath.Join(root, "folder", "nested", "file.bin"))
	if err != nil || !bytes.Equal(contents, payload) {
		t.Fatalf("uploaded contents = %q, err = %v", contents, err)
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
