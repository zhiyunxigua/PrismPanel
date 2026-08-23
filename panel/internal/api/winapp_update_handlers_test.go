package api

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"PrismPanel/internal/winappupdates"
)

func TestWinAppUpdateCheckAndDownload(t *testing.T) {
	repository, err := winappupdates.NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executable := []byte("MZ-test-winapp")
	bundle := apiTestWinAppBundle(t, "0.0.2", executable)
	if _, err := repository.Publish(bytes.NewReader(bundle), int64(len(bundle)), "bug fixes", winappupdates.Uploader{}); err != nil {
		t.Fatal(err)
	}
	server := &Server{winApp: repository}

	checkRequest := httptest.NewRequest(http.MethodGet, "/api/v1/winapp/update?version=0.0.1&platform=windows&arch=amd64", nil)
	checkResponse := httptest.NewRecorder()
	server.handleWinAppUpdate(checkResponse, checkRequest)
	if checkResponse.Code != http.StatusOK {
		t.Fatalf("update check status = %d", checkResponse.Code)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			UpdateAvailable bool              `json:"update_available"`
			Latest          winAppReleaseView `json:"latest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(checkResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Success || !payload.Data.UpdateAvailable || payload.Data.Latest.Version != "0.0.2" {
		t.Fatalf("unexpected update response: %#v", payload)
	}

	downloadRequest := httptest.NewRequest(http.MethodGet, payload.Data.Latest.DownloadURL, nil)
	downloadResponse := httptest.NewRecorder()
	server.handleWinAppReleaseDownload(downloadResponse, downloadRequest)
	if downloadResponse.Code != http.StatusOK || !bytes.Equal(downloadResponse.Body.Bytes(), executable) {
		t.Fatalf("download response = %d, %q", downloadResponse.Code, downloadResponse.Body.Bytes())
	}
}

func apiTestWinAppBundle(t *testing.T, version string, executable []byte) []byte {
	t.Helper()
	hash := sha256.Sum256(executable)
	manifest, err := json.Marshal(winappupdates.BuildManifest{
		SchemaVersion: 1, Product: "PrismPanel", Platform: "windows", Arch: "amd64",
		Version: version, File: "PrismPanel.exe", Size: int64(len(executable)),
		SHA256: hex.EncodeToString(hash[:]), BuiltAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, contents := range map[string][]byte{"manifest.json": manifest, "PrismPanel.exe": executable} {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
