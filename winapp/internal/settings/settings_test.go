package settings

import (
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "PrismPanel", "settings.json"))
	if err := store.Save(Settings{PanelURL: "https://panel.example.com"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PanelURL != "https://panel.example.com" {
		t.Fatalf("panel URL = %q", loaded.PanelURL)
	}
}

func TestStoreReturnsEmptySettingsWhenMissing(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "missing.json"))
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PanelURL != "" {
		t.Fatalf("panel URL = %q", loaded.PanelURL)
	}
}
