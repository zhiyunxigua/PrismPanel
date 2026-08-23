package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "a.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := copyDirectory(source, filepath.Join(root, "destination")); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "destination", "nested", "a.txt"))
	if err != nil || string(contents) != "hello" {
		t.Fatalf("copied contents = %q, err = %v", contents, err)
	}
}

func TestCopyDirectoryRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("outside"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside.txt"), filepath.Join(source, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := copyDirectory(source, filepath.Join(root, "destination")); err == nil {
		t.Fatal("copyDirectory accepted a symlink")
	}
}
