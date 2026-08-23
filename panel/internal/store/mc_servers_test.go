package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestSQLiteStore(t *testing.T) *Store {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	store := &Store{db: &database{DB: raw, prefix: "prism_", sqlite: true}}
	if err := store.initializeSchema(context.Background()); err != nil {
		t.Fatalf("initialize schema: %v", err)
	}
	return store
}

func TestMCServerCRUD(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	created, err := store.CreateMCServer(ctx, MCServerInput{
		Name: "国际服", ServerKey: "play.example.com:25565", Host: "play.example.com",
		Port: 25565, Note: "备注",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 || created.ServerKey != "play.example.com:25565" || created.LastStatus != MCServerStatusUnknown {
		t.Fatalf("created = %+v", created)
	}
	if !created.Enabled {
		t.Fatal("enabled should default to true")
	}

	// 重复 server_key → 冲突
	if _, err := store.CreateMCServer(ctx, MCServerInput{
		Name: "dup", ServerKey: "play.example.com:25565", Host: "play.example.com", Port: 25565,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	// 空 ServerKey 时自动规范化
	auto, err := store.CreateMCServer(ctx, MCServerInput{Name: "auto", Host: "MC.Example.Org", Port: 19132})
	if err != nil {
		t.Fatalf("create auto: %v", err)
	}
	if auto.ServerKey != "mc.example.org:19132" {
		t.Fatalf("auto key = %q", auto.ServerKey)
	}

	// 更新
	disabled := false
	updated, err := store.UpdateMCServer(ctx, created.ID, MCServerInput{
		Name: "新名", ServerKey: "new.example.com:25565", Host: "new.example.com", Port: 25566,
		Enabled: &disabled, Note: "新备注",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "新名" || updated.Host != "new.example.com" || updated.Port != 25566 ||
		updated.ServerKey != "new.example.com:25565" || updated.Enabled || updated.Note != "新备注" {
		t.Fatalf("updated = %+v", updated)
	}

	// 更新冲突（占用别的 key）
	other, err := store.CreateMCServer(ctx, MCServerInput{Name: "other", Host: "other.example.com", Port: 25565})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	if _, err := store.UpdateMCServer(ctx, other.ID, MCServerInput{
		Name: "other", ServerKey: "new.example.com:25565", Host: "new.example.com", Port: 25566,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected update conflict, got %v", err)
	}

	// 按 key 查询
	byKey, err := store.GetMCServerByKey(ctx, "new.example.com:25565")
	if err != nil {
		t.Fatalf("get by key: %v", err)
	}
	if byKey.ID != created.ID {
		t.Fatalf("by key id = %d, want %d", byKey.ID, created.ID)
	}

	// 列表（created 已禁用，auto 与 other 启用）
	servers, err := store.ListMCServers(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("list len = %d", len(servers))
	}
	enabledServers, err := store.ListEnabledMCServers(ctx)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabledServers) != 2 {
		t.Fatalf("enabled len = %d", len(enabledServers))
	}

	// 删除
	if err := store.DeleteMCServer(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetMCServer(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := store.DeleteMCServer(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete should be not found, got %v", err)
	}
}

func TestMCServerResultAndObservations(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := context.Background()

	server, err := store.CreateMCServer(ctx, MCServerInput{Name: "sv", Host: "127.0.0.1", Port: 25565})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	online := uint32(10)
	maximum := uint32(100)
	latency := uint32(23)
	if err := store.UpdateMCServerResult(ctx, server.ID, MCServerStatusOK, &online, &maximum, &latency, "1.20.4", ""); err != nil {
		t.Fatalf("update result: %v", err)
	}
	loaded, err := store.GetMCServer(ctx, server.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.LastStatus != MCServerStatusOK || *loaded.LastOnline != 10 || *loaded.LastMax != 100 ||
		*loaded.LastLatencyMS != 23 || loaded.LastVersion != "1.20.4" || loaded.LastCheckedAt == nil {
		t.Fatalf("loaded = %+v", loaded)
	}

	// 失败结果
	if err := store.UpdateMCServerResult(ctx, server.ID, MCServerStatusFailed, nil, nil, nil, "", "timeout"); err != nil {
		t.Fatalf("update failed result: %v", err)
	}
	failed, err := store.GetMCServer(ctx, server.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if failed.LastStatus != MCServerStatusFailed || failed.LastOnline != nil || failed.LastError != "timeout" {
		t.Fatalf("failed = %+v", failed)
	}

	// 观察点
	base := time.Now().UTC().Add(-10 * time.Minute)
	for index := 0; index < 3; index++ {
		observation := MCServerObservation{
			ServerID: server.ID, SampledAt: base.Add(time.Duration(index) * time.Minute),
			Online: uint32(5 + index), MaxPlayers: 100, LatencyMS: 20 + uint32(index),
			VersionName: "1.20.4", Protocol: 765,
		}
		if err := store.CreateMCServerObservation(ctx, observation); err != nil {
			t.Fatalf("create observation: %v", err)
		}
	}
	points, err := store.MCServerObservationsBetweenForServers(ctx, []uint64{server.ID},
		base.Add(-time.Minute), base.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("query observations: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("points = %d", len(points))
	}
	if points[0].Online != 5 || points[2].Online != 7 || points[1].LatencyMS != 21 ||
		points[0].ServerKey != "127.0.0.1:25565" || points[0].ServerName != "sv" {
		t.Fatalf("points = %+v", points)
	}

	// 空 id 列表 → 空结果
	empty, err := store.MCServerObservationsBetweenForServers(ctx, []uint64{}, base, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("query empty: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty points = %d", len(empty))
	}

	// 最新采样时间
	latest, err := store.LatestMCServerObservationTime(ctx, server.ID)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if !latest.Equal(points[2].SampledAt) {
		t.Fatalf("latest = %v, want %v", latest, points[2].SampledAt)
	}

	// 清理
	deleted, err := store.DeleteMCServerObservationsBefore(ctx, server.ID, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("delete observations: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d", deleted)
	}

	// 删除服务器 → 观察点级联删除（SQLite 需要外键启用，这里验证删除本身不报错即可）
	if err := store.DeleteMCServer(ctx, server.ID); err != nil {
		t.Fatalf("delete server: %v", err)
	}
}
