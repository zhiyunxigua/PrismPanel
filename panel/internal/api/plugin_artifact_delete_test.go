package api

// P3-1 回归测试：删除最后制品时 handler 返回成功（removed_plugin）并清理部署偏好；
// 删除非最后制品返回剩余插件；制品缺失（条目仍存在）不得误清偏好。
import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"PrismPanel/internal/config"
	"PrismPanel/internal/nodes"
	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/store"
)

func newArtifactDeleteTestServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	repository, err := panelplugins.NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storeRepo, err := store.Open(context.Background(), config.DatabaseConfig{
		Type: "sqlite", SQLitePath: ":memory:", TablePrefix: "prism_",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storeRepo.Close() })
	nodeService, err := nodes.NewService(storeRepo, nil, bytes.Repeat([]byte{0x5a}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		store: storeRepo, nodes: nodeService, plugins: repository,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, storeRepo, t.TempDir()
}

func uploadReviewPlugin(t *testing.T, repository *panelplugins.Repository, name, version string) panelplugins.UploadResult {
	t.Helper()
	result, err := repository.Upload(panelplugins.UploadInput{
		JARFilename: name + "-" + version + ".jar",
		JAR:         reviewTestJAR(t, name, version, "com.example.Main"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestLastArtifactDeleteSucceedsAndCleansPreferences 验证删除最后制品：
// 返回 200 + removed_plugin:true（前端依赖该响应），且部署偏好孤儿行被清除。
func TestLastArtifactDeleteSucceedsAndCleansPreferences(t *testing.T) {
	server, storeRepo, _ := newArtifactDeleteTestServer(t)
	uploaded := uploadReviewPlugin(t, server.plugins, "Example", "1.0")
	pluginID, artifactID := uploaded.Plugin.PluginID, uploaded.Artifact.ArtifactID
	// 预置部署偏好，验证删除后孤儿行被清理。
	if err := storeRepo.ReplacePluginDeployPreferences(context.Background(), "spigot", pluginID, []store.TargetRule{
		{NodeID: "node-a", ServerID: "lobby", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	artifactPart := strconv.FormatInt(artifactID, 10)
	request := httptest.NewRequest(http.MethodDelete,
		"/api/v1/plugins/spigot/"+pluginID+"/"+artifactPart+"?confirm_deployed=true", nil)
	request = withSuperAdminSession(request)
	recorder := httptest.NewRecorder()
	server.handlePluginArtifactDelete(recorder, request, "spigot", pluginID, artifactPart)

	if recorder.Code != http.StatusOK {
		t.Fatalf("last-artifact delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("removed_plugin")) {
		t.Fatalf("expected removed_plugin:true in body, got %s", recorder.Body.String())
	}
	left, err := storeRepo.PluginDeployPreferences(context.Background(), "spigot", pluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("deploy preferences must be cleaned after last-artifact delete: %#v", left)
	}
}

// TestNonLastArtifactDeleteKeepsPlugin 验证删除非最后制品：返回剩余插件且不清理偏好。
func TestNonLastArtifactDeleteKeepsPlugin(t *testing.T) {
	server, storeRepo, _ := newArtifactDeleteTestServer(t)
	first := uploadReviewPlugin(t, server.plugins, "Example", "1.0")
	second := uploadReviewPlugin(t, server.plugins, "Example", "2.0")
	pluginID := first.Plugin.PluginID
	if err := storeRepo.ReplacePluginDeployPreferences(context.Background(), "spigot", pluginID, []store.TargetRule{
		{NodeID: "node-a", ServerID: "lobby", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	artifactPart := strconv.FormatInt(first.Artifact.ArtifactID, 10)
	request := httptest.NewRequest(http.MethodDelete,
		"/api/v1/plugins/spigot/"+pluginID+"/"+artifactPart+"?confirm_deployed=true", nil)
	request = withSuperAdminSession(request)
	recorder := httptest.NewRecorder()
	server.handlePluginArtifactDelete(recorder, request, "spigot", pluginID, artifactPart)

	if recorder.Code != http.StatusOK {
		t.Fatalf("non-last artifact delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("removed_plugin")) {
		t.Fatalf("non-last artifact delete must not report removed_plugin: %s", recorder.Body.String())
	}
	left, err := storeRepo.PluginDeployPreferences(context.Background(), "spigot", pluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("preferences must be kept when plugin still exists: %#v", left)
	}
	_ = second
}

// TestMissingArtifactDoesNotWipePreferences 验证制品缺失（条目仍存在）时不误清偏好。
func TestMissingArtifactDoesNotWipePreferences(t *testing.T) {
	server, storeRepo, _ := newArtifactDeleteTestServer(t)
	uploaded := uploadReviewPlugin(t, server.plugins, "Example", "1.0")
	pluginID := uploaded.Plugin.PluginID
	if err := storeRepo.ReplacePluginDeployPreferences(context.Background(), "spigot", pluginID, []store.TargetRule{
		{NodeID: "node-a", ServerID: "lobby", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodDelete,
		"/api/v1/plugins/spigot/"+pluginID+"/999", nil)
	request = withSuperAdminSession(request)
	recorder := httptest.NewRecorder()
	server.handlePluginArtifactDelete(recorder, request, "spigot", pluginID, "999")

	// 制品不存在是错误（非 200），且不得清理仍存在插件的偏好。
	if recorder.Code == http.StatusOK {
		t.Fatalf("missing artifact delete must not succeed: %s", recorder.Body.String())
	}
	left, err := storeRepo.PluginDeployPreferences(context.Background(), "spigot", pluginID)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		t.Fatalf("preferences must NOT be wiped when artifact is missing but plugin exists: %#v", left)
	}
}

func reviewTestJAR(t *testing.T, name, version, main string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create("plugin.yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "name: "+name+"\nversion: "+version+"\nmain: "+main+"\nauthors: [Tester]\n"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
