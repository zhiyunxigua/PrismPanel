package game

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafePathSegmentCleansUnsafeCharacters(t *testing.T) {
	got := safePathSegment("a/b\\c:d*e?f")
	if got != "a-b-c-d-e-f" {
		t.Fatalf("safePathSegment mismatch: %q", got)
	}
	if safePathSegment("...") != "server" {
		t.Fatal("empty segment should fall back to server")
	}
}

func TestCopyFileCreatesTargetDirectories(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "nested", "dir", "target.txt")
	if err := copyFile(source, target); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "hello" {
		t.Fatalf("copy mismatch: %q", contents)
	}
}

func TestCopyDirectoryCopiesRecursively(t *testing.T) {
	source := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(source, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "sub", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "dst")
	if err := copyDirectory(source, target); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(target, "sub", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "a" {
		t.Fatalf("copy mismatch: %q", contents)
	}
}
