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
