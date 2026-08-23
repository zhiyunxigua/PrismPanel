package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFileProxyGrantIsBoundAndSingleUse(t *testing.T) {
	store := newFileProxyStore()
	token, err := store.Add(fileProxyGrant{
		DaemonTicket: "daemon-secret", NodeID: "node-a", Scope: "file.delete",
		UserID: "user-a", ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Consume(token, "user-b", "node-a", "file.delete"); err == nil {
		t.Fatal("expected user binding rejection")
	}
	grant, err := store.Consume(token, "user-a", "node-a", "file.delete")
	if err != nil || grant.DaemonTicket != "daemon-secret" {
		t.Fatalf("unexpected proxy grant: %#v, %v", grant, err)
	}
	if _, err := store.Consume(token, "user-a", "node-a", "file.delete"); err == nil {
		t.Fatal("expected single-use proxy grant rejection")
	}
}

func TestFileProxyGrantSupportsBoundedChunkRequests(t *testing.T) {
	store := newFileProxyStore()
	token, err := store.Add(fileProxyGrant{
		DaemonTicket: "daemon-secret", NodeID: "node-a", Scope: "file.upload",
		UserID: "user-a", ExpiresAt: time.Now().Add(time.Minute), MaxUses: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := store.Consume(token, "user-a", "node-a", "file.upload"); err != nil {
			t.Fatalf("consume chunk %d: %v", attempt, err)
		}
	}
	if _, err := store.Consume(token, "user-a", "node-a", "file.upload"); err == nil {
		t.Fatal("expected bounded proxy grant rejection")
	}
}

func TestProxyFileScopeRequiresExpectedMethod(t *testing.T) {
	if got := proxyFileScope("content", "GET"); got != "file.read" {
		t.Fatalf("content GET scope = %q", got)
	}
	if got := proxyFileScope("delete", "GET"); got != "" {
		t.Fatalf("delete GET unexpectedly mapped to %q", got)
	}
	if got := proxyFileScope("import", "POST"); got != "file.import" {
		t.Fatalf("import POST scope = %q", got)
	}
	if got := proxyFileScope("archive", "POST"); got != "file.archive" {
		t.Fatalf("archive POST scope = %q", got)
	}
	if got := proxyFileScope("archive", "GET"); got != "" {
		t.Fatalf("archive GET unexpectedly mapped to %q", got)
	}
	if got := proxyFileScope("extract", "POST"); got != "file.extract" {
		t.Fatalf("extract POST scope = %q", got)
	}
	if got := proxyFileScope("extract-status", "POST"); got != "file.extract.status" {
		t.Fatalf("extract-status POST scope = %q", got)
	}
	if got := proxyFileScope("upload-status", "POST"); got != "file.upload.status" {
		t.Fatalf("upload-status POST scope = %q", got)
	}
	if got := proxyFileScope("upload-cancel", "POST"); got != "file.upload.cancel" {
		t.Fatalf("upload-cancel POST scope = %q", got)
	}
}

func TestSanitizeInstanceMessagesProtectsPlayerAndPluginDetails(t *testing.T) {
	input := []json.RawMessage{json.RawMessage(`{
		"instance_id":"test","online_players":1,
		"players":[{"uuid":"player","name":"Steve"}],
		"plugins":[{"name":"PrismMC"}]
	}`)}
	result := sanitizeInstanceMessages(input, false, false)
	var item map[string]any
	if err := json.Unmarshal(result[0], &item); err != nil {
		t.Fatal(err)
	}
	if _, exists := item["players"]; exists {
		t.Fatal("expected player details to be removed")
	}
	if _, exists := item["plugins"]; exists {
		t.Fatal("expected plugin details to be removed")
	}
	if item["online_players"] != float64(1) {
		t.Fatal("expected aggregate player count to remain available")
	}
}
