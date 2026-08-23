package store

import (
	"path/filepath"
	"testing"

	"PrismPanel-daemon/internal/supervisor"
)

func TestOperatorStoreRoundTrip(t *testing.T) {
	directory := t.TempDir()
	storage := NewOperatorStore(directory)
	state := supervisor.OperatorRegistryState{
		Revision: 7,
		Sources: []supervisor.OperatorSource{{
			PanelID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Revision: 3,
			Operators: []supervisor.OperatorEntry{{
				UUID: "123e4567-e89b-12d3-a456-426614174000", Name: "Steve",
			}},
		}},
	}
	if err := storage.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := storage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != state.Revision || len(loaded.Sources) != 1 ||
		loaded.Sources[0].Operators[0].Name != "Steve" {
		t.Fatalf("unexpected loaded state: %+v", loaded)
	}
	if storage.path != filepath.Join(directory, "operators.json") {
		t.Fatalf("unexpected registry path: %s", storage.path)
	}
}
