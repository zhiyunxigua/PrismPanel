package api

import (
	"fmt"
	"reflect"
	"testing"
)

func TestNormalizeNetGamesPreference(t *testing.T) {
	preference, err := normalizeNetGamesPreference(netGamesPreference{
		SelectedGameIDs: []string{" game-a ", "", "game-b", "game-a"},
	})
	if err != nil {
		t.Fatalf("normalize preference: %v", err)
	}
	expected := []string{"game-a", "game-b"}
	if !reflect.DeepEqual(preference.SelectedGameIDs, expected) {
		t.Fatalf("selected ids = %#v, want %#v", preference.SelectedGameIDs, expected)
	}
}

func TestNormalizeNetGamesPreferenceRejectsMoreThanTwenty(t *testing.T) {
	ids := make([]string, 21)
	for index := range ids {
		ids[index] = fmt.Sprintf("game-%d", index)
	}
	if _, err := normalizeNetGamesPreference(netGamesPreference{SelectedGameIDs: ids}); err == nil {
		t.Fatal("expected selection limit error")
	}
}

func TestNormalizeLegacyNetGamesPreference(t *testing.T) {
	preference, err := normalizeLegacyNetGamesPreference(legacyNetGamesPreference{
		DisplayGameCount: 3,
		ForcedGameIDs:    []string{"game-a", " game-b ", "game-a"},
	})
	if err != nil {
		t.Fatalf("normalize legacy preference: %v", err)
	}
	expected := []string{"game-a", "game-b"}
	if !reflect.DeepEqual(preference.ForcedGameIDs, expected) {
		t.Fatalf("forced ids = %#v, want %#v", preference.ForcedGameIDs, expected)
	}
}
