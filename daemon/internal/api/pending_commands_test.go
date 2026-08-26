package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	pluginservice "PrismPanel-daemon/internal/plugins"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
)

// newPendingCommandTestAPI 构造带 pending 存储目录的测试环境。
func newPendingCommandTestAPI(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	manager, err := supervisor.NewManager(cfg, &eventbus.Bus{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	servers := serverservice.NewService(store.NewServerStore(filepath.Join(root, "servers")), manager, nil)
	dataDir := filepath.Join(root, "data")
	plugins, err := pluginservice.NewService(manager, servers, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{plugins: plugins}, dataDir
}

// writePendingQueue 直接向实例 pending 目录写入队列文件（绕过入队 API，简化测试）。
func writePendingQueue(t *testing.T, dataDir, instanceID, contents string) {
	t.Helper()
	directory := filepath.Join(dataDir, "plugin-pending", instanceID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "pending.json"), []byte(contents), 0o640); err != nil {
		t.Fatal(err)
	}
}

// TestPendingListCommand 验证 pending.list 命令：返回实例队列项（含状态/错误）。
func TestPendingListCommand(t *testing.T) {
	api, dataDir := newPendingCommandTestAPI(t)
	writePendingQueue(t, dataDir, "inst1", `[
		{"type":"enable","plugin_name":"Locked","directory":"plugins","attempts":1,"last_error":"file locked"}
	]`)

	raw, err := json.Marshal(map[string]any{"instance_id": "inst1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := api.executeFrom("", "pending.list", raw)
	if err != nil {
		t.Fatal(err)
	}
	view, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected pending.list result: %T", result)
	}
	if view["instance_id"] != "inst1" {
		t.Fatalf("unexpected instance_id: %v", view["instance_id"])
	}
	pending, ok := view["pending"].([]pluginservice.PendingItem)
	if !ok || len(pending) != 1 {
		t.Fatalf("unexpected pending items: %#v", view["pending"])
	}
	if pending[0].Status != "pending" || pending[0].PluginName != "Locked" ||
		pending[0].Attempts != 1 || pending[0].LastError != "file locked" {
		t.Fatalf("unexpected pending item view: %#v", pending[0])
	}
}

// TestPendingClearCommand 验证 pending.clear 命令：单条删除与整队清除。
func TestPendingClearCommand(t *testing.T) {
	api, dataDir := newPendingCommandTestAPI(t)
	writePendingQueue(t, dataDir, "inst1", `[
		{"type":"enable","plugin_name":"Alpha","directory":"plugins"},
		{"type":"enable","plugin_name":"Beta","directory":"plugins"}
	]`)

	// 单条删除（index=0）后只剩 Beta。
	raw, err := json.Marshal(map[string]any{"instance_id": "inst1", "index": 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.executeFrom("", "pending.clear", raw); err != nil {
		t.Fatal(err)
	}
	result, err := api.executeFrom("", "pending.list", mustJSON(t, map[string]any{"instance_id": "inst1"}))
	if err != nil {
		t.Fatal(err)
	}
	pending := result.(map[string]any)["pending"].([]pluginservice.PendingItem)
	if len(pending) != 1 || pending[0].PluginName != "Beta" {
		t.Fatalf("after single clear expected only Beta, got %#v", pending)
	}

	// 整队清除。
	raw, err = json.Marshal(map[string]any{"instance_id": "inst1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.executeFrom("", "pending.clear", raw); err != nil {
		t.Fatal(err)
	}
	result, err = api.executeFrom("", "pending.list", mustJSON(t, map[string]any{"instance_id": "inst1"}))
	if err != nil {
		t.Fatal(err)
	}
	view := result.(map[string]any)
	if len(view["pending"].([]pluginservice.PendingItem)) != 0 || len(view["failed"].([]pluginservice.PendingItem)) != 0 {
		t.Fatalf("queue should be empty after full clear: %#v", view)
	}

	// 参数校验：缺失 instance_id 与越界下标。
	if _, err := api.executeFrom("", "pending.clear", mustJSON(t, map[string]any{})); err == nil {
		t.Fatal("expected error for missing instance_id")
	}
	if _, err := api.executeFrom("", "pending.clear", mustJSON(t, map[string]any{"instance_id": "inst1", "index": 9})); err == nil {
		t.Fatal("expected error for out-of-range index")
	}
}

// TestPendingListInvalidInstanceID 验证非法实例 ID 被拒绝。
func TestPendingListInvalidInstanceID(t *testing.T) {
	api, _ := newPendingCommandTestAPI(t)
	if _, err := api.executeFrom("", "pending.list", mustJSON(t, map[string]any{"instance_id": "../evil"})); err == nil {
		t.Fatal("expected error for invalid instance id")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
