package plugins

import (
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/supervisor"
)

func TestMergeRuntimeOnlyPluginDoesNotRequireRestart(t *testing.T) {
	items, pending := merge(nil, []supervisor.LoadedPlugin{{
		Name: "Velocity", Version: "3.4.0", Enabled: true,
	}}, true)
	if pending {
		t.Fatal("runtime-only plugin unexpectedly requires restart")
	}
	if len(items) != 1 || items[0].Status != "runtime_only" || items[0].PendingRestart {
		t.Fatalf("unexpected runtime-only plugin result: %#v", items)
	}
}

func TestMergeMissingExternalPluginRequiresRestart(t *testing.T) {
	items, pending := merge(nil, []supervisor.LoadedPlugin{{
		Name: "Example", Version: "1.0.0", SourceFile: "Example.jar", Enabled: true,
	}}, true)
	if !pending {
		t.Fatal("missing external plugin should require restart")
	}
	if len(items) != 1 || items[0].Status != "uninstall_pending_restart" || !items[0].PendingRestart {
		t.Fatalf("unexpected missing external plugin result: %#v", items)
	}
}

func TestScanEnabledPluginHashesOnlyIncludesTopLevelEnabledJARs(t *testing.T) {
	workspace := t.TempDir()
	pluginDir := filepath.Join(workspace, "plugins")
	if err := os.MkdirAll(filepath.Join(pluginDir, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Example.jar":           "enabled",
		"Upper.JAR":             "upper",
		"Disabled.jar.disabled": "disabled",
		"notes.txt":             "ignored",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(pluginDir, name), []byte(contents), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "nested", "Nested.jar"), []byte("nested"), 0o640); err != nil {
		t.Fatal(err)
	}

	hashes, err := scanEnabledPluginHashes(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(hashes) != 2 || hashes["example.jar"] == "" || hashes["upper.jar"] == "" {
		t.Fatalf("unexpected enabled plugin hashes: %#v", hashes)
	}
}

func TestChangedPluginFilesDetectsAddedModifiedAndRemovedFiles(t *testing.T) {
	baseline := map[string]string{"same.jar": "same", "modified.jar": "old", "removed.jar": "removed"}
	current := map[string]string{"same.jar": "same", "modified.jar": "new", "added.jar": "added"}

	changes := changedPluginFiles(baseline, current)
	if len(changes) != 3 {
		t.Fatalf("unexpected changes: %#v", changes)
	}
	for _, name := range []string{"modified.jar", "removed.jar", "added.jar"} {
		if _, exists := changes[name]; !exists {
			t.Fatalf("expected %s to be reported as changed: %#v", name, changes)
		}
	}
}

func TestMergeMarksChangedPluginFilesPendingRestart(t *testing.T) {
	files := []FilePlugin{
		{Name: "Loaded", Version: "2.0", Main: "example.Loaded", SourceFile: "Loaded.jar", Enabled: true},
		{Name: "Added", Version: "1.0", Main: "example.Added", SourceFile: "Added.jar", Enabled: true},
	}
	runtime := []supervisor.LoadedPlugin{
		{Name: "Loaded", Version: "2.0", Main: "example.Loaded", SourceFile: "Loaded.jar", Enabled: true},
		{Name: "Removed", Version: "1.0", Main: "example.Removed", SourceFile: "Removed.jar", Enabled: true},
	}
	changes := map[string]struct{}{"loaded.jar": {}, "added.jar": {}, "removed.jar": {}}

	items, pending := merge(files, runtime, true, changes)
	if !pending {
		t.Fatal("changed plugin files should require restart")
	}
	statuses := make(map[string]string, len(items))
	for _, item := range items {
		statuses[item.Name] = item.Status
	}
	if statuses["Loaded"] != "update_pending_restart" {
		t.Fatalf("unexpected loaded status: %#v", statuses)
	}
	if statuses["Added"] != "install_pending_restart" {
		t.Fatalf("unexpected added status: %#v", statuses)
	}
	if statuses["Removed"] != "uninstall_pending_restart" {
		t.Fatalf("unexpected removed status: %#v", statuses)
	}
}
