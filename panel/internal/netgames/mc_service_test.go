package netgames

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"PrismPanel/internal/config"
	"PrismPanel/internal/store"
)

func newTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	repository, err := store.Open(context.Background(), config.DatabaseConfig{Type: "sqlite", SQLitePath: ":memory:"})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := NewService(repository, t.TempDir(), logger)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return service, repository
}

func TestMCServerCreateUpdateDelete(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()

	server, err := service.CreateMCServer(ctx, store.MCServerInput{Name: "我的世界国际服", Host: "play.example.com", Port: 25565, Note: "测试"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if server.ServerKey != "play.example.com:25565" {
		t.Fatalf("server key = %q", server.ServerKey)
	}
	if server.Name != "我的世界国际服" || server.Note != "测试" {
		t.Fatalf("server = %+v", server)
	}

	// 同地址重复创建 → 冲突
	if _, err := service.CreateMCServer(ctx, store.MCServerInput{Name: "dup", Host: "PLAY.Example.COM", Port: 25565}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	enabled := false
	updated, err := service.UpdateMCServer(ctx, server.ID, store.MCServerInput{
		Name: "新名字", Host: "new.example.com", Port: 19132, Enabled: &enabled, Note: "更新",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "新名字" || updated.Host != "new.example.com" || updated.Port != 19132 || updated.Enabled || updated.ServerKey != "new.example.com:19132" {
		t.Fatalf("updated = %+v", updated)
	}

	servers, err := service.MCServers(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("list len = %d", len(servers))
	}

	if err := service.DeleteMCServer(ctx, server.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := service.MCServer(ctx, server.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestMCServerCollectWritesObservation(t *testing.T) {
	service, repository := newTestService(t)
	ctx := context.Background()

	port, closeFn := startFakeModernMCServer(t, 12, 100, "1.20.4", 765)
	defer closeFn()

	server, err := repository.CreateMCServer(ctx, store.MCServerInput{Name: "fake", Host: "127.0.0.1", Port: port})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	summary, err := service.CollectMCNow(ctx)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if summary.Checked != 1 || summary.Online != 1 || summary.Failed != 0 {
		t.Fatalf("summary = %+v", summary)
	}

	loaded, err := repository.GetMCServer(ctx, server.ID)
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if loaded.LastStatus != store.MCServerStatusOK {
		t.Fatalf("last status = %q", loaded.LastStatus)
	}
	if loaded.LastOnline == nil || *loaded.LastOnline != 12 {
		t.Fatalf("last online = %v", loaded.LastOnline)
	}
	if loaded.LastVersion != "1.20.4" {
		t.Fatalf("last version = %q", loaded.LastVersion)
	}
	if loaded.LastCheckedAt == nil {
		t.Fatal("last checked at is nil")
	}

	// 采集 3 次后查询趋势
	for index := 0; index < 2; index++ {
		if _, err := service.CollectMCNow(ctx); err != nil {
			t.Fatalf("collect #%d: %v", index+2, err)
		}
	}
	from := time.Now().UTC().Add(-time.Hour)
	to := time.Now().UTC().Add(time.Hour)
	series, err := service.MCServerSeries(ctx, []string{server.ServerKey}, from, to)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series.Games) != 1 {
		t.Fatalf("series games = %d", len(series.Games))
	}
	game := series.Games[0]
	if game.GameID != server.ServerKey || game.Name != "fake" {
		t.Fatalf("game = %+v", game)
	}
	if len(game.Points) != 3 {
		t.Fatalf("points = %d, want 3", len(game.Points))
	}
	if game.Points[0].Value == nil || *game.Points[0].Value != 12 {
		t.Fatalf("first point = %+v", game.Points[0])
	}
	if game.Points[0].MaxPlayers != 100 || game.Points[0].VersionName != "1.20.4" {
		t.Fatalf("first point details = %+v", game.Points[0])
	}
}

func TestMCServerCollectFailure(t *testing.T) {
	service, repository := newTestService(t)
	ctx := context.Background()

	port, err := closedLocalPort()
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	server, err := repository.CreateMCServer(ctx, store.MCServerInput{Name: "dead", Host: "127.0.0.1", Port: port})
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	summary, err := service.CollectMCNow(ctx)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if summary.Failed != 1 || summary.Online != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	loaded, err := repository.GetMCServer(ctx, server.ID)
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if loaded.LastStatus != store.MCServerStatusFailed {
		t.Fatalf("last status = %q", loaded.LastStatus)
	}
	if loaded.LastError == "" {
		t.Fatal("expected last error message")
	}
	series, err := service.MCServerSeries(ctx, []string{server.ServerKey}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series.Games) != 1 || len(series.Games[0].Points) != 0 {
		t.Fatalf("failed server should not produce points, got %+v", series.Games)
	}
}

func TestMCServerSeriesSelectsByKey(t *testing.T) {
	service, repository := newTestService(t)
	ctx := context.Background()

	portA, closeA := startFakeModernMCServer(t, 3, 20, "1.19", 759)
	defer closeA()
	portB, closeB := startFakeModernMCServer(t, 8, 40, "1.20", 763)
	defer closeB()

	serverA, err := repository.CreateMCServer(ctx, store.MCServerInput{Name: "A", Host: "127.0.0.1", Port: portA})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	serverB, err := repository.CreateMCServer(ctx, store.MCServerInput{Name: "B", Host: "127.0.0.1", Port: portB})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if _, err := service.CollectMCNow(ctx); err != nil {
		t.Fatalf("collect: %v", err)
	}

	from := time.Now().UTC().Add(-time.Hour)
	to := time.Now().UTC().Add(time.Hour)
	// 只查 A
	series, err := service.MCServerSeries(ctx, []string{serverA.ServerKey}, from, to)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series.Games) != 1 || series.Games[0].GameID != serverA.ServerKey {
		t.Fatalf("games = %+v", series.Games)
	}
	// 不传 key → 全部
	seriesAll, err := service.MCServerSeries(ctx, []string{}, from, to)
	if err != nil {
		t.Fatalf("series all: %v", err)
	}
	if len(seriesAll.Games) != 2 {
		t.Fatalf("games all = %d", len(seriesAll.Games))
	}
	_ = serverB
}

// closedLocalPort 返回一个 127.0.0.1 上不可达的空闲端口（先监听再关闭）。
func closedLocalPort() (uint16, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	_ = listener.Close()
	return port, nil
}
