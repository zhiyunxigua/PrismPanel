package files

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/deployment"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
)

func TestFileLifecycleAndVersionConflict(t *testing.T) {
	service, target, root := newTestService(t)
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("server-port=25565\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	content, err := service.Read(target, "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Save(target, content.Path, "server-port=25566\n", content.Encoding, content.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version == content.Version {
		t.Fatal("file version did not change")
	}
	if _, err := service.Save(target, content.Path, "stale", content.Encoding, content.Version); err == nil {
		t.Fatal("expected stale file version to be rejected")
	}
	if err := service.Create(target, "plugins", "directory"); err != nil {
		t.Fatal(err)
	}
	uploaded, err := service.Upload(target, "plugins/example.jar", bytes.NewReader([]byte("jar")), 3, "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.Path != "plugins/example.jar" || uploaded.Size != 3 {
		t.Fatalf("unexpected upload result: %#v", uploaded)
	}
	uploadPath := filepath.Join(root, "plugins", "example.jar")
	baseVersion, err := fileVersion(uploadPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(uploadPath, []byte("changed"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(target, uploaded.Path, bytes.NewReader([]byte("local")), 5, "", true, baseVersion); err == nil {
		t.Fatal("expected stale upload version to be rejected")
	}
	currentVersion, err := fileVersion(uploadPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(target, uploaded.Path, bytes.NewReader([]byte("local")), 5, "", true, currentVersion); err != nil {
		t.Fatal(err)
	}
	if err := service.Move(target, uploaded.Path, "plugins/renamed.jar", false); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(target, []string{"plugins"}, false); err == nil {
		t.Fatal("expected recursive delete requirement")
	}
	if err := service.Delete(target, []string{"plugins"}, true); err != nil {
		t.Fatal(err)
	}
}

func TestUploadCreatesMissingParentDirectories(t *testing.T) {
	service, target, root := newTestService(t)
	entry, err := service.Upload(target, "nested/deep/file.bin", bytes.NewReader([]byte("data")), 4, "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path != "nested/deep/file.bin" {
		t.Fatalf("unexpected upload path: %q", entry.Path)
	}
	contents, err := os.ReadFile(filepath.Join(root, "nested", "deep", "file.bin"))
	if err != nil || string(contents) != "data" {
		t.Fatalf("uploaded contents = %q, err = %v", contents, err)
	}
	info, err := os.Stat(filepath.Join(root, "nested", "deep"))
	if err != nil || !info.IsDir() {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o750 {
		t.Fatalf("parent mode = %v", info.Mode().Perm())
	}
}

func TestSecurePathRejectsTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	if _, err := securePath(root, "../outside", false); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := securePath(root, "link/file.txt", true); err == nil {
		t.Fatal("expected symlink traversal to be rejected")
	}
}

func TestListUsesBoundedPagination(t *testing.T) {
	service, target, root := newTestService(t)
	for _, name := range []string{"b.txt", "a.txt", "folder"} {
		path := filepath.Join(root, name)
		if name == "folder" {
			if err := os.Mkdir(path, 0o750); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	results, err := service.List(target, []DirectoryRequest{{Path: ".", Limit: 2}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Entries) != 2 || results[0].Entries[0].Type != "directory" || !results[0].Truncated {
		t.Fatalf("unexpected first page: %#v", results[0])
	}
	second, err := service.List(target, []DirectoryRequest{{Path: ".", Limit: 2, Cursor: results[0].NextCursor}}, true)
	if err != nil || len(second[0].Entries) != 1 {
		t.Fatalf("unexpected second page: %#v, %v", second, err)
	}
}

func TestOpenDownloadArchivesDirectory(t *testing.T) {
	service, target, root := newTestService(t)
	if err := os.MkdirAll(filepath.Join(root, "plugins", "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plugins", "example.jar"), []byte("jar"), 0o640); err != nil {
		t.Fatal(err)
	}
	download, err := service.OpenDownload(target, "plugins")
	if err != nil {
		t.Fatal(err)
	}
	defer download.Close()
	if download.Name != "plugins.zip" {
		t.Fatalf("unexpected archive name: %s", download.Name)
	}
	reader, err := zip.NewReader(download.File, download.Size)
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]*zip.File, len(reader.File))
	for _, entry := range reader.File {
		entries[entry.Name] = entry
	}
	for _, expected := range []string{"plugins/", "plugins/empty/", "plugins/example.jar"} {
		if entries[expected] == nil {
			t.Fatalf("archive is missing %s: %#v", expected, entries)
		}
	}
	stream, err := entries["plugins/example.jar"].Open()
	if err != nil {
		t.Fatal(err)
	}
	contents, readErr := io.ReadAll(stream)
	closeErr := stream.Close()
	if readErr != nil || closeErr != nil || string(contents) != "jar" {
		t.Fatalf("unexpected archived contents: %q, %v, %v", contents, readErr, closeErr)
	}
}

func TestCreateDirectoryIsIdempotent(t *testing.T) {
	service, target, root := newTestService(t)
	if err := service.Create(target, "plugins", "directory"); err != nil {
		t.Fatal(err)
	}
	if err := service.Create(target, "plugins", "directory"); err != nil {
		t.Fatalf("recreating a directory should succeed: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "plugins"))
	if err != nil || !info.IsDir() {
		t.Fatalf("directory was not preserved: %#v, %v", info, err)
	}
}

func TestOpenDownloadRejectsDirectorySymlink(t *testing.T) {
	service, target, root := newTestService(t)
	if err := os.Mkdir(filepath.Join(root, "plugins"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "plugins", "link")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := service.OpenDownload(target, "plugins"); err == nil {
		t.Fatal("expected directory symlink to be rejected")
	}
}

func TestEnsureConfiguredDirectoryCreatesImagePath(t *testing.T) {
	base := filepath.Join(t.TempDir(), "group")
	created, err := ensureConfiguredDirectory(base, "images/lobby")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(created); err != nil || !info.IsDir() {
		t.Fatalf("image directory was not created: %v", err)
	}
}

func TestImportArchiveExtractsIntoEmptyWorkspace(t *testing.T) {
	service, target, root := newTestService(t)
	archive := makeZIP(t, []zipTestEntry{
		{name: "server.properties", contents: "server-port=25565\n"},
		{name: "plugins/", directory: true},
		{name: "plugins/example.jar", contents: "jar"},
	})
	result, err := service.ImportArchive(target, ".", bytes.NewReader(archive), int64(len(archive)), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 2 || result.Directories != 1 || result.Bytes != 21 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	contents, err := os.ReadFile(filepath.Join(root, "plugins", "example.jar"))
	if err != nil || string(contents) != "jar" {
		t.Fatalf("unexpected extracted file: %q, %v", contents, err)
	}
}

func TestImportArchiveRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry zipTestEntry
	}{
		{name: "traversal", entry: zipTestEntry{name: "../outside.txt", contents: "outside"}},
		{name: "symlink", entry: zipTestEntry{name: "link", contents: "target", mode: os.ModeSymlink | 0o777}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, target, root := newTestService(t)
			archive := makeZIP(t, []zipTestEntry{test.entry})
			if _, err := service.ImportArchive(target, ".", bytes.NewReader(archive), int64(len(archive)), ""); err == nil {
				t.Fatal("expected unsafe archive to be rejected")
			}
			assertDirectoryEmpty(t, root)
		})
	}
}

func TestImportArchiveRejectsExtractedSizeLimit(t *testing.T) {
	service, target, root := newTestService(t)
	service.maxExtract = 4
	archive := makeZIP(t, []zipTestEntry{{name: "large.txt", contents: "12345"}})
	if _, err := service.ImportArchive(target, ".", bytes.NewReader(archive), int64(len(archive)), ""); err == nil {
		t.Fatal("expected extracted size limit to be enforced")
	}
	assertDirectoryEmpty(t, root)
}

func TestImportArchiveRejectsNonEmptyWorkspace(t *testing.T) {
	service, target, root := newTestService(t)
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}
	archive := makeZIP(t, []zipTestEntry{{name: "new.txt", contents: "new"}})
	if _, err := service.ImportArchive(target, ".", bytes.NewReader(archive), int64(len(archive)), ""); err == nil {
		t.Fatal("expected non-empty workspace to be rejected")
	}
	contents, err := os.ReadFile(filepath.Join(root, "existing.txt"))
	if err != nil || string(contents) != "keep" {
		t.Fatalf("existing workspace was modified: %q, %v", contents, err)
	}
}

func TestImportArchiveFailureDoesNotPublishPartialFiles(t *testing.T) {
	service, target, root := newTestService(t)
	archive := makeZIP(t, []zipTestEntry{{name: "payload.txt", contents: "unique-payload"}})
	index := bytes.Index(archive, []byte("unique-payload"))
	if index < 0 {
		t.Fatal("could not locate stored ZIP payload")
	}
	archive[index] ^= 0xff
	if _, err := service.ImportArchive(target, ".", bytes.NewReader(archive), int64(len(archive)), ""); err == nil {
		t.Fatal("expected corrupt archive to be rejected")
	}
	assertDirectoryEmpty(t, root)
}

type zipTestEntry struct {
	name      string
	contents  string
	directory bool
	mode      os.FileMode
}

func makeZIP(t *testing.T, entries []zipTestEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		if entry.directory {
			header.SetMode(os.ModeDir | 0o750)
		} else if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory contains partial files: %#v", entries)
	}
}

func newTestService(t *testing.T) (*Service, Target, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	serverConfig := model.ServerConfig{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: "test", Name: "Test",
		Workspace: root, Port: 25565,
		Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}
	manager, err := supervisor.NewManager(cfg, &eventbus.Bus{}, []model.ServerConfig{serverConfig})
	if err != nil {
		t.Fatal(err)
	}
	serverService := serverservice.NewService(store.NewServerStore(t.TempDir()), manager, []model.ServerConfig{serverConfig})
	deployments := deployment.NewManager(serverService, manager, cfg.Files.CopyConcurrency)
	service := NewService(serverService, manager, deployments, cfg.Files.MaxEditFileSize, cfg.Files.MaxUploadFileSize, cfg.Files.MaxExtractedSize, cfg.Files.MaxArchiveDownloadSize, 2)
	return service, Target{Type: "instance", ID: "test"}, root
}
