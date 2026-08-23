package files

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/apperr"
)

func TestArchiveFile(t *testing.T) {
	service, target, root := newTestService(t)
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("server-port=25565\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	created, err := service.Archive(target, "server.properties", "server.properties.zip")
	if err != nil {
		t.Fatal(err)
	}
	if created.Path != "server.properties.zip" || created.Type != "file" || created.Size == 0 {
		t.Fatalf("unexpected archive entry: %#v", created)
	}
	assertZIPEntry(t, filepath.Join(root, created.Path), "server.properties", "server-port=25565\n")
}

func TestArchiveDirectory(t *testing.T) {
	service, target, root := newTestService(t)
	if err := os.MkdirAll(filepath.Join(root, "plugins", "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins", "example.jar"), []byte("jar"), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Archive(target, "plugins", "plugins.zip"); err != nil {
		t.Fatal(err)
	}
	assertZIPEntry(t, filepath.Join(root, "plugins.zip"), "plugins/example.jar", "jar")

	reader, err := zip.OpenReader(filepath.Join(root, "plugins.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := make(map[string]bool, len(reader.File))
	for _, entry := range reader.File {
		entries[entry.Name] = true
	}
	for _, expected := range []string{"plugins/", "plugins/empty/", "plugins/example.jar"} {
		if !entries[expected] {
			t.Fatalf("archive is missing %s: %#v", expected, entries)
		}
	}
}

func TestArchiveRejectsExistingAndNestedDestination(t *testing.T) {
	service, target, root := newTestService(t)
	if err := os.Mkdir(filepath.Join(root, "plugins"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "existing.zip"), []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Archive(target, "plugins", "existing.zip"); apperr.From(err).Code != "FILE_EXISTS" {
		t.Fatalf("expected FILE_EXISTS, got %v", err)
	}
	if _, err := service.Archive(target, "plugins", "plugins/nested.zip"); apperr.From(err).Code != "INVALID_REQUEST" {
		t.Fatalf("expected nested destination rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "nested.zip")); !os.IsNotExist(err) {
		t.Fatalf("nested archive was unexpectedly created: %v", err)
	}
}

func TestArchiveRejectsDirectorySymlink(t *testing.T) {
	service, target, root := newTestService(t)
	if err := os.Mkdir(filepath.Join(root, "plugins"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "plugins", "link")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := service.Archive(target, "plugins", "plugins.zip"); apperr.From(err).Code != "PATH_ESCAPE" {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins.zip")); !os.IsNotExist(err) {
		t.Fatalf("archive was unexpectedly published: %v", err)
	}
}

func assertZIPEntry(t *testing.T, archivePath, entryName, expected string) {
	t.Helper()
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		if entry.Name != entryName {
			continue
		}
		stream, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read archived entry: %v, %v", readErr, closeErr)
		}
		if string(contents) != expected {
			t.Fatalf("entry contents = %q, want %q", contents, expected)
		}
		return
	}
	t.Fatalf("archive is missing %s", entryName)
}
