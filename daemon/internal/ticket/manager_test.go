package ticket

import (
	"testing"
	"time"
)

func TestTicketScopeAndSingleUse(t *testing.T) {
	manager := NewManager()
	created, err := manager.Create("console.read", "lobby", time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Consume(created.Token, "console.read", "other"); err == nil {
		t.Fatal("expected instance scope rejection")
	}
	if _, err := manager.Consume(created.Token, "console.read", "lobby"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Consume(created.Token, "console.read", "lobby"); err == nil {
		t.Fatal("expected single-use rejection")
	}
}

func TestRestrictedTicketBindsMethodAndPaths(t *testing.T) {
	manager := NewManager()
	created, err := manager.CreateRestricted(RestrictedOptions{
		Scope: "file.move", ResourceType: "instance", ResourceID: "lobby",
		Path: "plugins/a.jar", Paths: []string{"plugins/a.jar", "plugins/b.jar"},
		Method: "POST", TTL: time.Minute, MaxUses: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ConsumeRestricted(created.Token, "file.move", "instance", "lobby", "plugins/c.jar", "POST"); err == nil {
		t.Fatal("expected unbound path to be rejected")
	}
	if _, err := manager.ConsumeRestricted(created.Token, "file.move", "instance", "lobby", "plugins/a.jar", "GET"); err == nil {
		t.Fatal("expected method mismatch to be rejected")
	}
	if _, err := manager.ConsumeRestricted(created.Token, "file.move", "instance", "lobby", "plugins/a.jar", "POST"); err != nil {
		t.Fatal(err)
	}
	if !created.AllowsPath("plugins/b.jar") {
		t.Fatal("expected destination path to be allowed")
	}
}

func TestTicketSourceBindingAndSessionRevocation(t *testing.T) {
	manager := NewManager()
	created, err := manager.CreateWithOptions("console.read", "lobby", time.Minute, 1, TicketOptions{
		ClientIP: "2001:db8::10", SessionID: "session-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ConsumeFrom(created.Token, "console.read", "lobby", "2001:db8::11"); err == nil {
		t.Fatal("expected source mismatch rejection")
	}
	if _, err := manager.ConsumeFrom(created.Token, "console.read", "lobby", "2001:db8::10"); err != nil {
		t.Fatal(err)
	}

	revoked, err := manager.CreateWithOptions("console.read", "lobby", time.Minute, 1, TicketOptions{
		SessionID: "session-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.RevokeSession("session-a")
	if _, err := manager.Consume(revoked.Token, "console.read", "lobby"); err == nil {
		t.Fatal("expected session revocation to invalidate ticket")
	}
}

func TestRestrictedTicketAllowsPluginUpload(t *testing.T) {
	manager := NewManager()
	created, err := manager.CreateRestricted(RestrictedOptions{
		Scope: "plugin.upload", ResourceType: "instance", ResourceID: "lobby",
		Path: ".", Method: "POST", MaxBytes: 12, SHA256: "abc", TTL: time.Minute, MaxUses: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ConsumeRestricted(created.Token, "plugin.upload", "instance", "lobby", ".", "POST"); err != nil {
		t.Fatal(err)
	}
}
