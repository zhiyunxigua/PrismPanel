package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"PrismPanel-daemon/internal/atomicfile"
	"PrismPanel-daemon/internal/supervisor"
)

type OperatorStore struct {
	path string
	mu   sync.Mutex
}

func NewOperatorStore(dataDir string) *OperatorStore {
	return &OperatorStore{path: filepath.Join(dataDir, "operators.json")}
}

func (s *OperatorStore) Load() (supervisor.OperatorRegistryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	contents, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return supervisor.OperatorRegistryState{Sources: []supervisor.OperatorSource{}}, nil
	}
	if err != nil {
		return supervisor.OperatorRegistryState{}, fmt.Errorf("read operator registry: %w", err)
	}
	var state supervisor.OperatorRegistryState
	if err := json.Unmarshal(contents, &state); err != nil {
		return supervisor.OperatorRegistryState{}, fmt.Errorf("decode operator registry: %w", err)
	}
	if state.Sources == nil {
		state.Sources = []supervisor.OperatorSource{}
	}
	return state, nil
}

func (s *OperatorStore) Save(state supervisor.OperatorRegistryState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode operator registry: %w", err)
	}
	contents = append(contents, '\n')
	if err := atomicfile.WriteFile(s.path, contents, 0o640); err != nil {
		return fmt.Errorf("write operator registry: %w", err)
	}
	return nil
}
