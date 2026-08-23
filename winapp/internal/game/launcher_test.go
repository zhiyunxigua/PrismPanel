package game

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitCommandLineKeepsQuotedClasspath(t *testing.T) {
	args, err := splitCommandLine(`-Xmx2G -cp "libraries/a.jar;libraries/b.jar" main.Class --name "Steve Jobs"`)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"-Xmx2G", "-cp", "libraries/a.jar;libraries/b.jar", "main.Class", "--name", "Steve Jobs"}
	if strings.Join(args, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("args mismatch\nwant: %q\n got: %q", expected, args)
	}
}

func TestEnsureDefaultOptionsWritesOnlyWhenMissing(t *testing.T) {
	dir := t.TempDir()
	if err := ensureDefaultOptions(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "options.txt")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "guiScale:2") {
		t.Fatalf("unexpected options content: %q", contents)
	}
	// 已存在时不覆盖
	if err := os.WriteFile(path, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaultOptions(dir); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != "custom" {
		t.Fatalf("options.txt was overwritten: %q", again)
	}
}

func TestUpsertFlagValueReplacesExistingValue(t *testing.T) {
	args := []string{"--gameDir", "old", "--server", "1.2.3.4"}
	got := upsertFlagValue(args, "--gameDir", "new")
	if len(got) != 4 || got[1] != "new" {
		t.Fatalf("upsert did not replace in place: %v", got)
	}
	got = upsertFlagValue(got, "--port", "25565")
	if len(got) != 6 || got[4] != "--port" || got[5] != "25565" {
		t.Fatalf("upsert did not append: %v", got)
	}
}

func TestDropUnresolvedPlaceholderArgsRemovesFlagAndValue(t *testing.T) {
	args := []string{"--username", "Steve", "--accessToken", "${auth_access_token}", "--uuid", "abc"}
	got := dropUnresolvedPlaceholderArgs(args)
	if len(got) != 4 {
		t.Fatalf("placeholder pair was not dropped: %v", got)
	}
	for _, arg := range got {
		if strings.Contains(arg, "${") {
			t.Fatalf("placeholder remains: %v", got)
		}
	}
}
