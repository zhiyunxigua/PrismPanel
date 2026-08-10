package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/apperr"
)

func TestCreateDirectoryDownloadEnforcesRawSizeLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload.txt"), []byte("12345"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := createDirectoryDownload(root, "workspace", 4)
	if err == nil {
		t.Fatal("expected directory download size limit")
	}
	if apperr.From(err).Code != "FILE_TOO_LARGE" {
		t.Fatalf("unexpected error: %v", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected missing source error: %v", err)
	}
}
