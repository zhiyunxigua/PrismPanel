package auth

import (
	"bytes"
	"testing"
)

func TestAPIKeyTokenRoundTrip(t *testing.T) {
	token, expectedHash, prefix, err := newAPIKeyToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) < 12 || token[:4] != "ppk_" {
		t.Fatalf("unexpected API key format: %q", token)
	}
	if prefix != token[:12] {
		t.Fatalf("prefix = %q, want %q", prefix, token[:12])
	}
	actualHash, err := apiKeyTokenHash(token)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualHash, expectedHash) {
		t.Fatal("API key hash did not round-trip")
	}
}

func TestAPIKeyTokenRejectsInvalidValues(t *testing.T) {
	for _, token := range []string{"", "password", "ppk_short", "ppk_!!!!"} {
		if _, err := apiKeyTokenHash(token); err == nil {
			t.Fatalf("expected %q to be rejected", token)
		}
	}
}
