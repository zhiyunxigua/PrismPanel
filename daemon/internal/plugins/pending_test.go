package plugins

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
)

// TestPendingApplySkipsFailedAndContinues 验证单条永久失败不再阻塞队首：
// 失败项记录到 failed 侧写并跳过，后续项继续执行，队列最终排空（drained）。
func TestPendingApplySkipsFailedAndContinues(t *testing.T) {
	store, err := newPendingStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alpha", "Beta", "Gamma"} {
		if err := store.enqueue("inst1", pendingOperation{
			Type: "enable", PluginName: name, Directory: "plugins",
		}, ""); err != nil {
			t.Fatal(err)
		}
	}

	failures := map[string]string{"Alpha": "permanent failure A", "Gamma": "permanent failure C"}
	drained, err := store.apply("inst1", func(operation pendingOperation, _ string) error {
		if message, ok := failures[operation.PluginName]; ok {
			return errors.New(message)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !drained {
		t.Fatal("queue should be drained: failed items were skipped and the rest applied")
	}
	pending, failed, err := store.list("inst1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending queue should be empty, got %#v", pending)
	}
	if len(failed) != 2 {
		t.Fatalf("failed sidecar should hold 2 items, got %#v", failed)
	}
	byName := make(map[string]pendingOperation, len(failed))
	for _, item := range failed {
		byName[item.PluginName] = item
	}
	for _, name := range []string{"Alpha", "Gamma"} {
		item, ok := byName[name]
		if !ok {
			t.Fatalf("failed sidecar missing %s: %#v", name, failed)
		}
		if item.Attempts != 1 || item.LastError == "" || item.FailedAt.IsZero() {
			t.Fatalf("failed item %s must record attempts/error/failed_at: %#v", name, item)
		}
	}
	if _, ok := byName["Beta"]; ok {
		t.Fatalf("successful item Beta must not be in failed sidecar: %#v", byName)
	}
}

// TestPendingApplyRetriesTransientUpToThreshold 验证 transient 失败保留重试语义，
// 但连续失败达到阈值后移入 failed 侧写并跳过，避免队列无限毒化。
func TestPendingApplyRetriesTransientUpToThreshold(t *testing.T) {
	store, err := newPendingStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.enqueue("inst1", pendingOperation{
		Type: "enable", PluginName: "Locked", Directory: "plugins",
	}, ""); err != nil {
		t.Fatal(err)
	}
	transient := func(pendingOperation, string) error { return os.ErrPermission }

	for attempt := 1; attempt < pendingRetryThreshold; attempt++ {
		drained, err := store.apply("inst1", transient)
		if err != nil {
			t.Fatal(err)
		}
		if drained {
			t.Fatalf("attempt %d: transient failure below threshold must keep the item for retry", attempt)
		}
		pending, failed, err := store.list("inst1")
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 || pending[0].Attempts != attempt || pending[0].LastError == "" || len(failed) != 0 {
			t.Fatalf("attempt %d: unexpected queue state: pending=%#v failed=%#v", attempt, pending, failed)
		}
	}

	drained, err := store.apply("inst1", transient)
	if err != nil {
		t.Fatal(err)
	}
	if !drained {
		t.Fatal("after reaching retry threshold the queue must be drained")
	}
	pending, failed, err := store.list("inst1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 || len(failed) != 1 {
		t.Fatalf("unexpected final state: pending=%#v failed=%#v", pending, failed)
	}
	if failed[0].Attempts != pendingRetryThreshold {
		t.Fatalf("failed item should record %d attempts, got %d", pendingRetryThreshold, failed[0].Attempts)
	}
	if failed[0].FailedAt.IsZero() {
		t.Fatalf("failed item should record failed_at: %#v", failed[0])
	}
}

// TestPendingMixedRetryableAndApplied 验证混合场景：可重试项保留、成功项出队、队列未排空。
func TestPendingMixedRetryableAndApplied(t *testing.T) {
	store, err := newPendingStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.enqueue("inst1", pendingOperation{
		Type: "enable", PluginName: "Locked", Directory: "plugins",
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.enqueue("inst1", pendingOperation{
		Type: "enable", PluginName: "Fine", Directory: "plugins",
	}, ""); err != nil {
		t.Fatal(err)
	}
	drained, err := store.apply("inst1", func(operation pendingOperation, _ string) error {
		if operation.PluginName == "Locked" {
			return os.ErrPermission
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if drained {
		t.Fatal("queue must not be drained while a retryable item remains")
	}
	pending, failed, err := store.list("inst1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].PluginName != "Locked" || pending[0].Attempts != 1 {
		t.Fatalf("unexpected pending queue: %#v", pending)
	}
	if len(failed) != 0 {
		t.Fatalf("no item should be failed yet: %#v", failed)
	}
}

// TestPendingClearSingleAndAll 验证 pending.clear 的单条删除与整队清除。
func TestPendingClearSingleAndAll(t *testing.T) {
	store, err := newPendingStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		if err := store.enqueue("inst1", pendingOperation{
			Type: "enable", PluginName: name, Directory: "plugins",
		}, ""); err != nil {
			t.Fatal(err)
		}
	}
	// 制造一条 failed 侧写记录：Alpha 永久失败，Beta 成功出队。
	if _, err := store.apply("inst1", func(operation pendingOperation, _ string) error {
		if operation.PluginName == "Alpha" {
			return errors.New("permanent failure")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 单条删除 failed 侧写。
	failedIndex := 0
	if err := store.clear("inst1", nil, &failedIndex); err != nil {
		t.Fatal(err)
	}
	_, failed, err := store.list("inst1")
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 0 {
		t.Fatalf("failed sidecar should be empty after single clear, got %#v", failed)
	}

	// 重新入队两条，单条删除 pending 队列：Beta 保留。
	for _, name := range []string{"Beta", "Gamma"} {
		if err := store.enqueue("inst1", pendingOperation{
			Type: "enable", PluginName: name, Directory: "plugins",
		}, ""); err != nil {
			t.Fatal(err)
		}
	}
	index := 0
	if err := store.clear("inst1", &index, nil); err != nil {
		t.Fatal(err)
	}
	pending, _, err := store.list("inst1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].PluginName != "Gamma" {
		t.Fatalf("single clear should keep Gamma, got %#v", pending)
	}

	// 越界下标报错。
	outOfRange := 5
	if err := store.clear("inst1", &outOfRange, nil); err == nil {
		t.Fatal("expected out-of-range error for pending index")
	}

	// 整队清除（index/failedIndex 均为 nil）清空实例目录。
	if err := store.clear("inst1", nil, nil); err != nil {
		t.Fatal(err)
	}
	pending, failed, err = store.list("inst1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 || len(failed) != 0 {
		t.Fatalf("queue should be fully cleared: pending=%#v failed=%#v", pending, failed)
	}
	if _, err := os.Stat(filepath.Join(store.root, "inst1")); !os.IsNotExist(err) {
		t.Fatalf("instance pending directory should be removed after full clear, stat err=%v", err)
	}
}

// TestPendingListAndClearCommands 验证服务层 pending.list / pending.clear 管理命令行为。
func TestPendingListAndClearCommands(t *testing.T) {
	manager, err := supervisor.NewManager(config.Config{}, &eventbus.Bus{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	servers := serverservice.NewService(store.NewServerStore(t.TempDir()), manager, nil)
	service, err := NewService(manager, servers, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// 队列中的 enable 项因插件不存在而永久失败 → 移入 failed 侧写 → 队列排空。
	workspace := t.TempDir()
	if err := service.pending.enqueue("inst1", pendingOperation{
		Type: "enable", PluginName: "Missing", Directory: "plugins",
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.applyPending("inst1", workspace); err != nil {
		t.Fatalf("applyPending should succeed when all items are skipped to failed: %v", err)
	}

	result, err := service.PendingList("inst1")
	if err != nil {
		t.Fatal(err)
	}
	view, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected pending.list result type: %T", result)
	}
	if view["instance_id"] != "inst1" {
		t.Fatalf("unexpected instance_id: %v", view["instance_id"])
	}
	pendingItems, ok := view["pending"].([]PendingItem)
	if !ok || len(pendingItems) != 0 {
		t.Fatalf("pending should be empty: %#v", view["pending"])
	}
	failedItems, ok := view["failed"].([]PendingItem)
	if !ok || len(failedItems) != 1 {
		t.Fatalf("failed should hold 1 item: %#v", view["failed"])
	}
	item := failedItems[0]
	if item.Status != "failed" || item.PluginName != "Missing" || item.Attempts != 1 || item.LastError == "" {
		t.Fatalf("unexpected failed item view: %#v", item)
	}

	// pending.clear 整队清除后列表应为空。
	if err := service.PendingClear("inst1", nil, nil); err != nil {
		t.Fatal(err)
	}
	all, err := service.PendingList("")
	if err != nil {
		t.Fatal(err)
	}
	instances, ok := all.(map[string]any)["instances"].([]map[string]any)
	if !ok || len(instances) != 0 {
		t.Fatalf("no instances should have pending queues after clear: %#v", all)
	}

	// 参数校验：空 instance_id 与非法实例 ID。
	if err := service.PendingClear("", nil, nil); err == nil {
		t.Fatal("expected error for empty instance_id")
	}
	if _, err := service.PendingList("../evil"); err == nil {
		t.Fatal("expected error for invalid instance id")
	}
}
