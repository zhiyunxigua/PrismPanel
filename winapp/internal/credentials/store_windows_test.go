//go:build windows

package credentials

import "testing"

func TestCredentialTargetsAreScopedByPanelAndCanonicalUsername(t *testing.T) {
	left := accountTargetPrefix("https://panel-a.example")
	right := accountTargetPrefix("https://panel-b.example")
	if left == right {
		t.Fatal("different panels share a credential target prefix")
	}
	if panelScope("HTTPS://PANEL-A.EXAMPLE") != panelScope("https://panel-a.example") {
		t.Fatal("panel credential scopes are not canonicalized")
	}
	if accountID("Admin") != accountID(" admin ") {
		t.Fatal("username account IDs are not canonicalized")
	}
	if !validAccountID(accountID("admin")) || validAccountID("admin") {
		t.Fatal("account ID validation is inconsistent")
	}
}
