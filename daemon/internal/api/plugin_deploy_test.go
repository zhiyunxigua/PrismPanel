package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
	"PrismPanel-daemon/internal/model"
	pluginservice "PrismPanel-daemon/internal/plugins"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
	"PrismPanel-daemon/internal/ticket"
)

// newDeployTestAPI 构造与 upload_test.go 同构的部署测试环境（supervisor + servers + plugins）。
func newDeployTestAPI(t *testing.T) (*Server, *ticket.Manager, string, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	serverID := "group_1"
	workspace := filepath.Join(root, serverID)
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatal(err)
	}
	configs := []model.ServerConfig{{
		SchemaVersion: model.SchemaVersion, Type: "standalone", ServerID: serverID, Name: serverID,
		Workspace: workspace, Port: 25565,
		Process: model.ProcessConfig{StartCommand: "server", StopCommand: "stop", StopTimeoutSeconds: 30},
		Console: model.ConsoleConfig{Encoding: "utf-8"},
	}}
	manager, err := supervisor.NewManager(cfg, &eventbus.Bus{}, configs)
	if err != nil {
		t.Fatal(err)
	}
	servers := serverservice.NewService(store.NewServerStore(filepath.Join(root, "servers")), manager, configs)
	plugins, err := pluginservice.NewService(manager, servers, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	tickets := ticket.NewManager()
	return &Server{tickets: tickets, plugins: plugins}, tickets, serverID, workspace
}

// deployBundleBytes 生成合法的 plugin 部署 bundle（plugin.jar + manifest.yaml，sha256 与 jar 一致）。
func deployBundleBytes(t *testing.T, jar []byte, filename string) []byte {
	t.Helper()
	sum := sha256.Sum256(jar)
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create("plugin.jar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(jar); err != nil {
		t.Fatal(err)
	}
	manifest := "kind: plugin\nplugin_type: spigot\nname: Example\nversion: 2.0\nmain: com.example.Main\n" +
		"artifact:\n  original_filename: " + filename + "\n  sha256: " + hex.EncodeToString(sum[:]) + "\n"
	entry, err = archive.Create("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, manifest); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func deployTestJAR(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	entry, err := archive.Create("plugin.yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "name: Example\nversion: 2.0\nmain: com.example.Main\nauthors: [Tester]\n"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// TestPluginDeployChunkedRequestAccepted 验证 chunked（ContentLength=-1）部署请求不再因
// written != ContentLength 恒成立而报 size mismatch：合法内容应完整走通部署链路。
func TestPluginDeployChunkedRequestAccepted(t *testing.T) {
	api, tickets, serverID, workspace := newDeployTestAPI(t)
	payload := deployBundleBytes(t, deployTestJAR(t), "Example-2.0.jar")
	sum := sha256.Sum256(payload)
	created, err := tickets.CreateUpload("plugin.deploy", serverID, hex.EncodeToString(sum[:]), int64(len(payload)), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/deploy?server_id="+serverID, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+created.Token)
	request.ContentLength = -1 // chunked 传输
	request.TransferEncoding = []string{"chunked"}
	recorder := httptest.NewRecorder()

	api.handlePluginDeploy(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("chunked deploy status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || !response.Success {
		t.Fatalf("chunked deploy did not succeed: %s", recorder.Body.String())
	}
	installed := filepath.Join(workspace, "plugins", "Example-2.0.jar")
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("bundle was not deployed: %v", err)
	}
}

// TestPluginDeployChunkedRequestHashMismatch 验证 chunked 请求下 sha256 校验仍然生效：
// 内容错误时报 PLUGIN_HASH_MISMATCH，而不是 size mismatch。
func TestPluginDeployChunkedRequestHashMismatch(t *testing.T) {
	api, tickets, serverID, _ := newDeployTestAPI(t)
	payload := deployBundleBytes(t, deployTestJAR(t), "Example-2.0.jar")
	sum := sha256.Sum256(payload)
	created, err := tickets.CreateUpload("plugin.deploy", serverID, hex.EncodeToString(sum[:]), int64(len(payload)), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	corrupted := append([]byte{}, payload...)
	corrupted[len(corrupted)/2] ^= 0xff // 篡改中间字节（zip 尾字节可能本来就是 0x00）
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/deploy?server_id="+serverID, bytes.NewReader(corrupted))
	request.Header.Set("Authorization", "Bearer "+created.Token)
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	recorder := httptest.NewRecorder()

	api.handlePluginDeploy(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("chunked hash mismatch status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("PLUGIN_HASH_MISMATCH")) {
		t.Fatalf("expected PLUGIN_HASH_MISMATCH, got %s", recorder.Body.String())
	}
}

// TestPluginDeployChunkedRequestOversized 验证 chunked 请求仍受 MaxBytes 上限约束。
func TestPluginDeployChunkedRequestOversized(t *testing.T) {
	api, tickets, serverID, _ := newDeployTestAPI(t)
	payload := deployBundleBytes(t, deployTestJAR(t), "Example-2.0.jar")
	sum := sha256.Sum256(payload)
	created, err := tickets.CreateUpload("plugin.deploy", serverID, hex.EncodeToString(sum[:]), int64(len(payload)-1), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/deploy?server_id="+serverID, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+created.Token)
	request.ContentLength = -1
	request.TransferEncoding = []string{"chunked"}
	recorder := httptest.NewRecorder()

	api.handlePluginDeploy(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunked deploy status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("size does not match ticket")) {
		t.Fatalf("expected size error, got %s", recorder.Body.String())
	}
}

// TestPluginDeployDeclaredLengthMismatchStillRejected 验证显式声明 ContentLength 的普通请求
// 仍强制长度相等校验（既有行为不变）。
func TestPluginDeployDeclaredLengthMismatchStillRejected(t *testing.T) {
	api, tickets, serverID, _ := newDeployTestAPI(t)
	payload := deployBundleBytes(t, deployTestJAR(t), "Example-2.0.jar")
	sum := sha256.Sum256(payload)
	created, err := tickets.CreateUpload("plugin.deploy", serverID, hex.EncodeToString(sum[:]), int64(len(payload))+10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/deploy?server_id="+serverID, bytes.NewReader(payload))
	request.Header.Set("Authorization", "Bearer "+created.Token)
	request.ContentLength = int64(len(payload)) + 5 // 声明长度与实际不符
	recorder := httptest.NewRecorder()

	api.handlePluginDeploy(recorder, request)

	if recorder.Code == http.StatusOK {
		t.Fatalf("declared-length mismatch unexpectedly succeeded")
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("size does not match ticket")) {
		t.Fatalf("expected size mismatch error, got %s", recorder.Body.String())
	}
}
