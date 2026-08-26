package game

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"PrismPanel-winapp/internal/credentials"
)

// fakeCredStore 内存版面板凭据存储（仅记录 ClearAll 调用）。
type fakeCredStore struct {
	cleared string
}

func (f *fakeCredStore) List(string) ([]credentials.Account, error) { return nil, nil }
func (f *fakeCredStore) Get(string, string) (credentials.Credential, error) {
	return credentials.Credential{}, credentials.ErrNotFound
}
func (f *fakeCredStore) Save(string, string, string, time.Time) (credentials.Account, error) {
	return credentials.Account{}, nil
}
func (f *fakeCredStore) Delete(string, string) error { return nil }
func (f *fakeCredStore) ClearAll(panelURL string) error {
	f.cleared = panelURL
	return nil
}
func (f *fakeCredStore) AutoLoginAccount(string) (string, error)  { return "", nil }
func (f *fakeCredStore) SetAutoLoginAccount(string, string) error { return nil }

func withEnv(t *testing.T, key, value string) {
	t.Helper()
	previous, existed := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, previous)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// TestListCacheEntriesShape 清单应包含全部已知项且 id 唯一、说明非空。
func TestListCacheEntriesShape(t *testing.T) {
	entries := ListCacheEntries(&fakeCredStore{}, "https://panel.example.com")
	wantIDs := []string{
		cacheIDMCAccount, cacheIDPanelAccounts, cacheIDGameCache,
		cacheIDJavaRuntime, cacheIDAuthlibInjector, cacheIDDevLog,
	}
	if len(entries) != len(wantIDs) {
		t.Fatalf("expected %d cache entries, got %d", len(wantIDs), len(entries))
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.ID == "" || seen[entry.ID] {
			t.Errorf("duplicate or empty cache id %q", entry.ID)
		}
		seen[entry.ID] = true
		if entry.Name == "" || entry.Description == "" {
			t.Errorf("entry %q missing name/description", entry.ID)
		}
	}
	for _, id := range wantIDs {
		if !seen[id] {
			t.Errorf("cache entry %q missing from list", id)
		}
	}
}

// TestClearUnknownCacheID 未知 id 应返回失败项而不是报错/panic。
func TestClearUnknownCacheID(t *testing.T) {
	results := ClearCacheEntries(&fakeCredStore{}, "", []string{"no-such-cache", "", "no-such-cache"})
	if len(results) != 1 {
		t.Fatalf("expected 1 deduplicated result, got %d", len(results))
	}
	if results[0].OK {
		t.Fatalf("unknown cache id should fail, got %+v", results[0])
	}
	if results[0].Error == "" {
		t.Fatal("unknown cache id should carry an error message")
	}
}

// TestSafeRemovePathSafety 路径校验：只允许删除白名单根目录内的路径。
func TestSafeRemovePathSafety(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "java")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := safeRemovePath(root, inside); err != nil {
		t.Fatalf("removing path inside root should be allowed: %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatal("inside path should have been removed")
	}

	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := safeRemovePath(root, outside); err == nil {
		t.Fatal("removing path outside root must be rejected")
	}

	traversal := filepath.Join(root, "..", "escape")
	if err := safeRemovePath(root, traversal); err == nil {
		t.Fatal("traversal path must be rejected")
	}

	if err := safeRemovePath(root, root); err == nil {
		t.Fatal("removing the whitelist root itself must be rejected")
	}
}

// TestDirSize 目录大小递归统计。
func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "one.bin"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "two.bin"), []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if size := dirSize(dir); size != 8 {
		t.Fatalf("expected dir size 8, got %d", size)
	}
}

// TestClearGameCacheRoot 使用 LOCALAPPDATA 重定向 UserCacheDir：
// 列出存在项 → 删除整个 game-cache → 目录消失。
func TestClearGameCacheRoot(t *testing.T) {
	base := t.TempDir()
	withEnv(t, "LocalAppData", base)
	cacheRoot := filepath.Join(base, "PrismPanel", "game-cache")
	if err := os.MkdirAll(filepath.Join(cacheRoot, "downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "downloads", "x.jar"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := DefaultCachePaths()
	if err != nil {
		t.Fatalf("DefaultCachePaths: %v", err)
	}
	if !strings.HasPrefix(paths.Root, base) {
		t.Skipf("UserCacheDir not redirected (got %s), skipping", paths.Root)
	}

	entries := ListCacheEntries(&fakeCredStore{}, "")
	var gameCache *CacheEntry
	for i := range entries {
		if entries[i].ID == cacheIDGameCache {
			gameCache = &entries[i]
		}
	}
	if gameCache == nil {
		t.Fatal("game-cache entry missing")
	}
	if !gameCache.Exists {
		t.Fatalf("game-cache should exist at %s", gameCache.Path)
	}
	if gameCache.SizeBytes != 5 {
		t.Fatalf("expected game-cache size 5, got %d", gameCache.SizeBytes)
	}

	results := ClearCacheEntries(&fakeCredStore{}, "", []string{cacheIDGameCache})
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("game-cache clear failed: %+v", results)
	}
	if _, err := os.Stat(cacheRoot); !os.IsNotExist(err) {
		t.Fatalf("game-cache root should be removed, stat err=%v", err)
	}
	// 白名单父目录（UserCacheDir/PrismPanel）保留。
	if _, err := os.Stat(filepath.Dir(cacheRoot)); err != nil {
		t.Fatalf("PrismPanel parent dir should remain, err=%v", err)
	}
}

// TestClearJavaRuntimeKeepsMinecraft 删除 java 缓存不触碰 .minecraft 版本内容。
func TestClearJavaRuntimeKeepsMinecraft(t *testing.T) {
	root := t.TempDir()
	withEnv(t, "PRISMPANEL_MC_DIR", root)
	javaDir := filepath.Join(root, "java")
	versionsDir := filepath.Join(root, "1.20.4", ".minecraft", "versions", "1.20.4")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(versionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(versionsDir, "1.20.4.json")
	if err := os.WriteFile(sentinel, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	results := ClearCacheEntries(&fakeCredStore{}, "", []string{cacheIDJavaRuntime})
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("java-runtime clear failed: %+v", results)
	}
	if _, err := os.Stat(javaDir); !os.IsNotExist(err) {
		t.Fatalf("java dir should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf(".minecraft version content must be preserved, err=%v", err)
	}
}

// TestClearDevLogCache 通过 PRISMPANEL_MC_DIR 重定向游戏目录后清空 dev-mode.log。
func TestClearDevLogCache(t *testing.T) {
	root := t.TempDir()
	withEnv(t, "PRISMPANEL_MC_DIR", root)
	devLogMu.Lock()
	devLogFilePath = ""
	devLogFile = nil
	devLogRing = nil
	devLogMu.Unlock()

	logPath := DevLogPath()
	if logPath == "" || !strings.HasPrefix(logPath, root) {
		t.Fatalf("dev log should be under redirected root, got %q", logPath)
	}
	if err := os.WriteFile(logPath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := ListCacheEntries(&fakeCredStore{}, "")
	for _, entry := range entries {
		if entry.ID == cacheIDDevLog && !entry.Exists {
			t.Fatalf("dev-log entry should report exists at %s", entry.Path)
		}
	}

	results := ClearCacheEntries(&fakeCredStore{}, "", []string{cacheIDDevLog})
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("dev-log clear failed: %+v", results)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("dev-mode.log should be removed, stat err=%v", err)
	}
	devLogMu.Lock()
	ringLen := len(devLogRing)
	devLogMu.Unlock()
	if ringLen != 0 {
		t.Fatalf("dev log ring should be empty, got %d entries", ringLen)
	}
}

// TestClearMCCredentialNoError 无账号时清理不应报错（删除不存在条目视为成功）。
func TestClearMCCredentialNoError(t *testing.T) {
	if err := ClearMCCredential(); err != nil {
		t.Fatalf("clearing missing mc credential should be a no-op, got %v", err)
	}
}

// TestPanelAccountsClear 面板账号：未配置面板不可清理；配置后调用 ClearAll 且传入面板地址。
func TestPanelAccountsClear(t *testing.T) {
	store := &fakeCredStore{}

	results := ClearCacheEntries(store, "", []string{cacheIDPanelAccounts})
	if len(results) != 1 || results[0].OK {
		t.Fatalf("panel accounts without panel URL must fail: %+v", results)
	}
	if store.cleared != "" {
		t.Fatalf("ClearAll must not be called without panel URL, got %q", store.cleared)
	}

	results = ClearCacheEntries(store, "https://panel.example.com", []string{cacheIDPanelAccounts})
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("panel accounts clear failed: %+v", results)
	}
	if store.cleared != "https://panel.example.com" {
		t.Fatalf("ClearAll should receive panel URL, got %q", store.cleared)
	}
}
