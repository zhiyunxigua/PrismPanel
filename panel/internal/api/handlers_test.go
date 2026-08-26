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

func TestServerListPayloadValid(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		valid bool
	}{
		{name: "nil payload", raw: "", valid: false},
		{name: "null payload", raw: "null", valid: false},
		{name: "empty object", raw: "{}", valid: false},
		{name: "missing servers field", raw: `{"instances":[]}`, valid: false},
		{name: "null servers", raw: `{"servers":null,"instances":[]}`, valid: false},
		{name: "empty servers array", raw: `{"servers":[],"instances":[]}`, valid: true},
		{name: "populated servers", raw: `{"servers":[{"server_id":"a"}],"instances":[]}`, valid: true},
		{name: "malformed json", raw: `{not-json`, valid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serverListPayloadValid(json.RawMessage(tc.raw)); got != tc.valid {
				t.Fatalf("serverListPayloadValid(%q) = %v, want %v", tc.raw, got, tc.valid)
			}
		})
	}
}
