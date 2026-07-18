package plugins

import (
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
