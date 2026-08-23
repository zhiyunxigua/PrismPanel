package fileopen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUploadUsesRetryableChunks(t *testing.T) {
	payload := []byte("payload")
	localPath := filepath.Join(t.TempDir(), "config.bin")
	if err := os.WriteFile(localPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	var offsets []string
	var finals []string
	var uploaded []byte
	failFirst := true
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/files/authorize":
			var input map[string]any
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input["chunked"] != true || input["expected_version"] != "base-version" {
				t.Errorf("unexpected authorization: %#v", input)
			}
			writeTestJSON(writer, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
				"mode": "proxy", "endpoint": "/upload", "ticket": "ticket",
				"resource_type": "instance", "resource_id": "server-a", "path": "config.bin", "chunk_size": 3,
			}})
		case "/upload":
			offset := request.Header.Get("X-Prism-Upload-Offset")
			offsets = append(offsets, offset)
			finals = append(finals, request.Header.Get("X-Prism-Upload-Final"))
			chunk, err := io.ReadAll(request.Body)
			if err != nil {
				t.Error(err)
			}
			if failFirst {
				failFirst = false
				writeTestJSON(writer, http.StatusInternalServerError, map[string]any{"success": false, "error": map[string]any{"code": "TEMPORARY", "message": "retry"}})
				return
			}
			uploaded = append(uploaded, chunk...)
			writeTestJSON(writer, http.StatusOK, map[string]any{"success": true, "data": map[string]any{}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	service := New(t.TempDir(), nil)
	task := &watchTask{
		input:   Input{NodeID: "node-a", ResourceType: "instance", ResourceID: "server-a", Path: "config.bin", Name: "config.bin", Size: int64(len(payload))},
		runtime: Runtime{APIBaseURL: server.URL, ProxySession: "session"}, localPath: localPath, baseVersion: "base-version",
	}
	if err := service.upload(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(offsets, []string{"0", "0", "3", "6"}) {
		t.Fatalf("unexpected chunk offsets: %#v", offsets)
	}
	if !reflect.DeepEqual(finals, []string{"false", "false", "false", "true"}) {
		t.Fatalf("unexpected final markers: %#v", finals)
	}
	if !reflect.DeepEqual(uploaded, payload) {
		t.Fatalf("unexpected uploaded payload: %q", uploaded)
	}
	digest := sha256.Sum256(payload)
	if task.baseVersion != hex.EncodeToString(digest[:]) {
		t.Fatalf("base version was not updated: %s", task.baseVersion)
	}
}

func writeTestJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
