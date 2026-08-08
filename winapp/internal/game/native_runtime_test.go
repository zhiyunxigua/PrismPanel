package game

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstallNetEaseRuntimeDownloadsRepairsAndReusesVerifiedCache(t *testing.T) {
	runtime := testAMD64PE(0x5a)
	checksum := testSHA256(runtime)
	var downloads atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest":
			_ = json.NewEncoder(response).Encode(fantnelUpdateResponse{
				Code: 1,
				Data: []fantnelUpdateFile{{
					Path:   "resources/" + netEaseRuntimeDLL,
					Size:   int64(len(runtime)),
					URL:    server.URL + "/files/" + netEaseRuntimeDLL,
					SHA256: checksum,
				}},
			})
		case "/files/" + netEaseRuntimeDLL:
			downloads.Add(1)
			_, _ = response.Write(runtime)
		default:
			http.NotFound(response, request)
		}
	}))
	client := server.Client()
	client.Timeout = 2 * time.Second
	source := testRuntimeSource(t, server.URL)
	paths := CachePaths{Root: t.TempDir()}
	target := filepath.Join(paths.Root, "native-runtime", netEaseRuntimeDLL)

	if err := installNetEaseRuntime(context.Background(), paths, client, source); err != nil {
		t.Fatal(err)
	}
	assertRuntimeContent(t, target, runtime)
	if downloads.Load() != 1 {
		t.Fatalf("download count = %d, want 1", downloads.Load())
	}

	if err := installNetEaseRuntime(context.Background(), paths, client, source); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatalf("verified cache was downloaded again: count=%d", downloads.Load())
	}

	if err := os.WriteFile(target, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installNetEaseRuntime(context.Background(), paths, client, source); err != nil {
		t.Fatal(err)
	}
	assertRuntimeContent(t, target, runtime)
	if downloads.Load() != 2 {
		t.Fatalf("corrupt cache was not repaired: count=%d", downloads.Load())
	}

	server.Close()
	if err := installNetEaseRuntime(context.Background(), paths, client, source); err != nil {
		t.Fatalf("verified cache was not reused offline: %v", err)
	}
}

func TestInstallNetEaseRuntimeRejectsChecksumMismatch(t *testing.T) {
	runtime := testAMD64PE(0x42)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/manifest":
			_ = json.NewEncoder(response).Encode(fantnelUpdateResponse{
				Code: 1,
				Data: []fantnelUpdateFile{{
					Path:   "resources/" + netEaseRuntimeDLL,
					Size:   int64(len(runtime)),
					URL:    server.URL + "/files/" + netEaseRuntimeDLL,
					SHA256: strings.Repeat("0", 64),
				}},
			})
		case "/files/" + netEaseRuntimeDLL:
			_, _ = response.Write(runtime)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	paths := CachePaths{Root: t.TempDir()}
	err := installNetEaseRuntime(context.Background(), paths, server.Client(), testRuntimeSource(t, server.URL))
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if fileExists(filepath.Join(paths.Root, "native-runtime", netEaseRuntimeDLL)) {
		t.Fatal("invalid runtime was installed")
	}
}

func TestFetchFantnelRuntimeManifestRejectsUntrustedDownloadHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(response).Encode(fantnelUpdateResponse{
			Code: 1,
			Data: []fantnelUpdateFile{{
				Path:   "resources/" + netEaseRuntimeDLL,
				Size:   512,
				URL:    "http://example.com/" + netEaseRuntimeDLL,
				SHA256: strings.Repeat("0", 64),
			}},
		})
	}))
	defer server.Close()

	_, err := fetchFantnelRuntimeManifest(context.Background(), server.Client(), testRuntimeSource(t, server.URL))
	if err == nil || !strings.Contains(err.Error(), "untrusted") {
		t.Fatalf("expected untrusted URL error, got %v", err)
	}
}

func testRuntimeSource(t *testing.T, serverURL string) netEaseRuntimeSource {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	return netEaseRuntimeSource{
		ManifestURL:  serverURL + "/manifest",
		DownloadHost: parsed.Host,
	}
}

func testAMD64PE(marker byte) []byte {
	contents := bytes.Repeat([]byte{marker}, 512)
	contents[0] = 0x4d
	contents[1] = 0x5a
	binary.LittleEndian.PutUint32(contents[0x3c:0x40], 0x80)
	copy(contents[0x80:0x84], []byte{0x50, 0x45, 0, 0})
	binary.LittleEndian.PutUint16(contents[0x84:0x86], 0x8664)
	return contents
}

func testSHA256(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func assertRuntimeContent(t *testing.T, path string, expected []byte) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents, expected) {
		t.Fatal("cached NetEase runtime content mismatch")
	}
	checksum, err := os.ReadFile(path + ".sha256")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(checksum)) != testSHA256(expected) {
		t.Fatal("cached NetEase runtime checksum mismatch")
	}
}
