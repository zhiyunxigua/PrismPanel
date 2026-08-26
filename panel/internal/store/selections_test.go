package store

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newSQLiteTestStore(t *testing.T) *Store {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	store := &Store{db: &database{DB: raw, prefix: "prism_", sqlite: true}}
	if err := store.initializeSchema(context.Background()); err != nil {
		t.Fatalf("initialize schema on sqlite: %v", err)
	}
	return store
}

// TestRemovePluginDeployPreferences 验证条目删除后部署偏好孤儿行被清除：
// 只删除目标 (plugin_type, plugin_id) 的行，不影响其它插件，无行时是幂等空操作。
func TestRemovePluginDeployPreferences(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()
	rules := []TargetRule{
		{NodeID: "node-a", ServerID: "lobby", Enabled: true},
		{NodeID: "node-b", Enabled: false},
	}
	if err := store.ReplacePluginDeployPreferences(ctx, "spigot", "essentials", rules); err != nil {
		t.Fatalf("seed essentials rules: %v", err)
	}
	if err := store.ReplacePluginDeployPreferences(ctx, "spigot", "other-plugin", rules); err != nil {
		t.Fatalf("seed other-plugin rules: %v", err)
	}

	if err := store.RemovePluginDeployPreferences(ctx, "spigot", "essentials"); err != nil {
		t.Fatalf("remove preferences: %v", err)
	}

	left, err := store.PluginDeployPreferences(ctx, "spigot", "essentials")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("orphan preferences still present after removal: %#v", left)
	}
	other, err := store.PluginDeployPreferences(ctx, "spigot", "other-plugin")
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != len(rules) {
		t.Fatalf("unrelated plugin preferences were affected: %#v", other)
	}
	if err := store.RemovePluginDeployPreferences(ctx, "spigot", "missing"); err != nil {
		t.Fatalf("removing preferences for unknown plugin must be a no-op: %v", err)
	}
}
