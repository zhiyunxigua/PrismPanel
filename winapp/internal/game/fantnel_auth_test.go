package game

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFantnelAuthenticatorBuildsAuthenticatedProfile(t *testing.T) {
	var received joinServerProfile
	var receivedServerID string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/fantnel/authenticated" {
			t.Errorf("endpoint path mismatch: %s", request.URL.Path)
		}
		receivedServerID = request.URL.Query().Get("id")
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":1,"msg":"ok"}`))
	}))
	defer server.Close()

	root := t.TempDir()
	baseMC := filepath.Join(root, ".minecraft")
	datPath := filepath.Join(baseMC, "versions", "1.21.8", "1.21.8.dat")
	if err := os.MkdirAll(filepath.Dir(datPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(datPath, []byte("dat"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := NewNetGameLaunchProfile(ServerConfig{
		GameID: "4661334467366178884", Version: Version1_21_8, VersionLabel: "1.21.8",
	}, "1.7.0")
	profile.ModInfo = `{"mods":[{"modPath":"a.jar","id":"a.jar","iid":"a","md5":"ABC"}]}`
	authenticator := &FantnelAuthenticator{Endpoint: server.URL, HTTPClient: server.Client()}
	ok, err := authenticator.Authenticate(context.Background(), profile.GameID, "123", "server/id", profile, AccountState{
		UserID: "123", UserToken: "token",
	}, baseMC)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("authentication should succeed")
	}
	if receivedServerID != "server/id" {
		t.Fatalf("server id mismatch: %q", receivedServerID)
	}
	if received.GameID != profile.GameID || received.GameVersion != "1.21.8" {
		t.Fatalf("profile mismatch: %+v", received)
	}
	if received.Profile.User.UserID != "123" || received.Profile.User.Token != "token" {
		t.Fatalf("user profile mismatch: %+v", received.Profile.User)
	}
	if len(received.Mods.Mods) != 1 || !strings.EqualFold(received.Mods.Mods[0].MD5, "ABC") {
		t.Fatalf("mod info mismatch: %+v", received.Mods)
	}
}
