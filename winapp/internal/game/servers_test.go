package game

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerStoreUpdatePreservesIdentity(t *testing.T) {
	store := NewServerStore(filepath.Join(t.TempDir(), "servers.json"))
	created, err := store.Create(ServerConfigInput{
		Name: "old", GameID: "4661334467366178884", IP: "127.0.0.1", Port: 25565,
		Username: "Steve", Version: Version1_20, ModDir: filepath.Join(t.TempDir(), "old-resources"),
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.Update(created.ID, ServerConfigInput{
		Name: "new", GameID: "4661334467366178884", IP: "10.0.0.2", Port: 25566,
		Username: "Alex", Version: Version1_20, ModDir: filepath.Join(t.TempDir(), "new-resources"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("server identity changed: created=%+v updated=%+v", created, updated)
	}
	if updated.Name != "new" || updated.IP != "10.0.0.2" || updated.Port != 25566 || updated.Username != "Alex" {
		t.Fatalf("server fields were not updated: %+v", updated)
	}
	loaded, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != updated {
		t.Fatalf("stored server mismatch: want=%+v got=%+v", updated, loaded)
	}
}

func TestNormalizeServerInputRequiresCustomResourceDirectory(t *testing.T) {
	_, err := normalizeServerInput(ServerConfigInput{
		Name: "server", GameID: "4661334467366178884", IP: "127.0.0.1", Port: 25565,
		Username: "Steve", Version: Version1_20,
	})
	if err == nil {
		t.Fatal("missing custom resource directory should be rejected")
	}
}

func TestRuntimeDirectoryUsesGameIDAndRoleName(t *testing.T) {
	paths := CachePaths{Runtime: filepath.Join("cache", "runtime")}
	server := ServerConfig{ID: "random-hash", GameID: "4661334467366178884", Username: "西瓜"}
	got := RuntimeDirectory(paths, server)
	want := filepath.Join(paths.Runtime, "4661334467366178884-西瓜")
	if got != want {
		t.Fatalf("runtime directory mismatch: want=%s got=%s", want, got)
	}
}

func TestMergeModDirectoryCopiesAllResourceKindsAndOverrides(t *testing.T) {
	source := filepath.Join(t.TempDir(), "resources")
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	for _, directory := range []string{"mods", "config", "resourcepacks", "shaderpacks"} {
		sourceFile := filepath.Join(source, directory, "shared.txt")
		runtimeFile := filepath.Join(runtimeDir, directory, "shared.txt")
		if err := os.MkdirAll(filepath.Dir(sourceFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(runtimeFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sourceFile, []byte("custom-"+directory), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(runtimeFile, []byte("base-"+directory), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := mergeModDirectory(source, runtimeDir); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"mods", "config", "resourcepacks", "shaderpacks"} {
		assertFileContent(t, filepath.Join(runtimeDir, directory, "shared.txt"), "custom-"+directory)
	}
}
func TestServerStoreUpdateMissingServer(t *testing.T) {
	store := NewServerStore(filepath.Join(t.TempDir(), "servers.json"))
	_, err := store.Update("missing", ServerConfigInput{
		Name: "server", GameID: "4661334467366178884", IP: "127.0.0.1", Port: 25565,
		Username: "Steve", Version: Version1_20, ModDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("updating a missing server should fail")
	}
}
