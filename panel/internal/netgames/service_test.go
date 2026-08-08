package netgames

import (
	"encoding/json"
	"testing"
)

func TestFirstNetGameImage(t *testing.T) {
	raw, err := json.Marshal([]string{"", " https://example.com/first.png ", "https://example.com/second.png"})
	if err != nil {
		t.Fatalf("marshal images: %v", err)
	}
	if image := firstNetGameImage(raw); image != "https://example.com/first.png" {
		t.Fatalf("first image = %q", image)
	}
	invalid, err := json.Marshal(map[string]bool{"invalid": true})
	if err != nil {
		t.Fatalf("marshal invalid payload: %v", err)
	}
	if image := firstNetGameImage(invalid); image != "" {
		t.Fatalf("invalid image payload returned %q", image)
	}
}
