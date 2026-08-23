package supervisor

import (
	"context"
	"testing"

	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/eventbus"
)

const (
	operatorPanelA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	operatorPanelB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	operatorUUIDA  = "123e4567-e89b-12d3-a456-426614174000"
	operatorUUIDB  = "223e4567-e89b-12d3-a456-426614174000"
)

func TestOperatorCatalogMergesPanelSources(t *testing.T) {
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var saved OperatorRegistryState
	if err := manager.ConfigureOperators(OperatorRegistryState{}, func(state OperatorRegistryState) error {
		saved = cloneOperatorState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReplaceOperatorSource(context.Background(), OperatorSource{
		PanelID: operatorPanelA, Revision: 1,
		Operators: []OperatorEntry{{UUID: operatorUUIDA, Name: "Steve"}, {UUID: operatorUUIDB, Name: "Alex"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReplaceOperatorSource(context.Background(), OperatorSource{
		PanelID: operatorPanelB, Revision: 1,
		Operators: []OperatorEntry{{UUID: operatorUUIDB, Name: "Alex"}},
	}); err != nil {
		t.Fatal(err)
	}
	manager.operatorMu.RLock()
	catalog := operatorCatalog(manager.operators)
	manager.operatorMu.RUnlock()
	if !catalog.Active || len(catalog.Operators) != 2 {
		t.Fatalf("unexpected merged catalog: %+v", catalog)
	}
	if len(saved.Sources) != 2 {
		t.Fatalf("expected both sources to be persisted: %+v", saved)
	}

	if _, err := manager.RemoveOperatorSource(context.Background(), operatorPanelA); err != nil {
		t.Fatal(err)
	}
	manager.operatorMu.RLock()
	catalog = operatorCatalog(manager.operators)
	manager.operatorMu.RUnlock()
	if len(catalog.Operators) != 1 || catalog.Operators[0].UUID != operatorUUIDB {
		t.Fatalf("unexpected catalog after removing panel A: %+v", catalog)
	}
}

func TestOperatorSourceRevisionIsMonotonic(t *testing.T) {
	manager, err := NewManager(config.Default(), &eventbus.Bus{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureOperators(OperatorRegistryState{}, nil); err != nil {
		t.Fatal(err)
	}
	source := OperatorSource{
		PanelID: operatorPanelA, Revision: 2,
		Operators: []OperatorEntry{{UUID: operatorUUIDA, Name: "Steve"}},
	}
	if _, err := manager.ReplaceOperatorSource(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	source.Revision = 1
	if _, err := manager.ReplaceOperatorSource(context.Background(), source); err == nil {
		t.Fatal("expected stale operator source revision to be rejected")
	}
}
