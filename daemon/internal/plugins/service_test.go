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

func TestMergeModLoadedByFile(t *testing.T) {
	files := []FilePlugin{
		{ID: "sodium", Name: "Sodium", Version: "0.5.11+mc1.21.1", SourceFile: "sodium-fabric-0.5.11+mc1.21.1.jar", Enabled: true},
	}
	runtime := []supervisor.LoadedPlugin{
		{ID: "sodium", Name: "Sodium", Version: "0.5.11+mc1.21.1", SourceFile: "sodium-fabric-0.5.11+mc1.21.1.jar", Enabled: true},
	}
	items, pending := merge(files, runtime, true)
	if pending || len(items) != 1 {
		t.Fatalf("unexpected mod merge result: %#v pending=%v", items, pending)
	}
	item := items[0]
	if !item.Loaded || item.Status != "loaded" || item.RuntimeVersion != "0.5.11+mc1.21.1" || item.FilePresent != true {
		t.Fatalf("unexpected loaded mod state: %#v", item)
	}
}

func TestMergeModMatchedByIDWhenNameDiffers(t *testing.T) {
	// 文件态显示名与运行态显示名不一致（例如运行时改了显示名），但 id 一致时必须按 id 匹配。
	files := []FilePlugin{
		{ID: "sodium", Name: "Sodium (file)", Version: "0.5.11", SourceFile: "sodium-fabric.jar", Enabled: true},
	}
	runtime := []supervisor.LoadedPlugin{
		{ID: "sodium", Name: "Sodium", Version: "0.5.11", SourceFile: "sodium-fabric.jar", Enabled: true},
	}
	items, _ := merge(files, runtime, true)
	if len(items) != 1 || !items[0].Loaded || items[0].Status != "loaded" {
		t.Fatalf("mod must be matched by id: %#v", items)
	}
}

func TestMergeModVersionMismatchRequiresRestart(t *testing.T) {
	files := []FilePlugin{
		{ID: "sodium", Name: "Sodium", Version: "0.6.0", SourceFile: "sodium-fabric-0.6.0.jar", Enabled: true},
	}
	runtime := []supervisor.LoadedPlugin{
		{ID: "sodium", Name: "Sodium", Version: "0.5.11", SourceFile: "sodium-fabric-0.5.11.jar", Enabled: true},
	}
	items, pending := merge(files, runtime, true)
	if !pending || len(items) != 1 {
		t.Fatalf("unexpected version mismatch result: %#v pending=%v", items, pending)
	}
	item := items[0]
	if item.Status != "update_pending_restart" || !item.PendingRestart {
		t.Fatalf("unexpected mismatch status: %#v", item)
	}
	if !containsIssue(item.Issues, "version_mismatch") {
		t.Fatalf("expected version_mismatch issue: %#v", item.Issues)
	}
	if item.RuntimeVersion != "0.5.11" {
		t.Fatalf("runtime version not captured: %#v", item)
	}
}

func TestMergeModDisabledFileStillLoadedRequiresRestart(t *testing.T) {
	files := []FilePlugin{
		{ID: "example", Name: "Example", Version: "1.0", SourceFile: "Example.jar.disabled", Enabled: false},
	}
	runtime := []supervisor.LoadedPlugin{
		{ID: "example", Name: "Example", Version: "1.0", SourceFile: "Example.jar", Enabled: true},
	}
	items, pending := merge(files, runtime, true)
	if !pending || len(items) != 1 {
		t.Fatalf("unexpected disabled-pending result: %#v pending=%v", items, pending)
	}
	item := items[0]
	if item.Status != "disabled_pending_restart" || !item.PendingRestart {
		t.Fatalf("unexpected disabled pending status: %#v", item)
	}
	if !containsIssue(item.Issues, "disabled_file_still_loaded") {
		t.Fatalf("expected disabled_file_still_loaded issue: %#v", item.Issues)
	}
}

func TestMergeModNotLoadedWhenConnected(t *testing.T) {
	files := []FilePlugin{
		{ID: "broken", Name: "Broken", Version: "1.0", SourceFile: "broken.jar", Enabled: true},
	}
	items, pending := merge(files, nil, true)
	if pending || len(items) != 1 || items[0].Status != "not_loaded" {
		t.Fatalf("enabled mod missing at runtime must be not_loaded: %#v pending=%v", items, pending)
	}
	if items[0].Loaded {
		t.Fatalf("not_loaded mod must not be flagged loaded: %#v", items[0])
	}
}

func TestFilterModReporterRemovesPrismFabricOnly(t *testing.T) {
	files := []FilePlugin{
		{ID: "sodium", Name: "Sodium", Version: "1.0", SourceFile: "sodium.jar", Enabled: true},
		{ID: modReporterID, Name: "Prism Fabric", Version: "0.2.0", SourceFile: "prism-fabric-0.2.0.jar", Enabled: true},
	}
	runtime := []supervisor.LoadedPlugin{
		{ID: "sodium", Name: "Sodium", Version: "1.0", SourceFile: "sodium.jar", Enabled: true},
		{ID: modReporterID, Name: "Prism Fabric", Version: "0.2.0", SourceFile: "prism-fabric-0.2.0.jar", Enabled: true},
	}
	items, pending := merge(files, runtime, true)
	if pending || len(items) != 2 {
		t.Fatalf("unexpected merge before filter: %#v pending=%v", items, pending)
	}
	filtered := filterModReporter(items)
	if len(filtered) != 1 || filtered[0].ID != "sodium" {
		t.Fatalf("prism-fabric must be filtered out: %#v", filtered)
	}
	if hasPendingRestart(filtered) {
		t.Fatal("the remaining mod must not require restart")
	}
}

func TestFilterModReporterDropsPendingOfPrismFabric(t *testing.T) {
	// prism-fabric 自身的 file_changed_since_start 不应让整个列表 pending。
	files := []FilePlugin{
		{ID: modReporterID, Name: "Prism Fabric", Version: "0.2.0", SourceFile: "prism-fabric-0.2.0.jar", Enabled: true},
	}
	changes := map[string]struct{}{"prism-fabric-0.2.0.jar": {}}
	items, pending := merge(files, nil, true, changes)
	if !pending {
		t.Fatal("prism-fabric file change should be pending before filtering")
	}
	filtered := filterModReporter(items)
	if len(filtered) != 0 {
		t.Fatalf("expected empty list after filter: %#v", filtered)
	}
	if hasPendingRestart(filtered) {
		t.Fatal("prism-fabric pending must not leak after filtering")
	}
}

func containsIssue(issues []string, expected string) bool {
	for _, issue := range issues {
		if issue == expected {
			return true
		}
	}
	return false
}
