package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"chatgpt-codex-proxy/internal/accounts"
)

type JSONAccountsStore struct {
	path string
	mu   sync.Mutex
}

var _ accounts.Store = (*JSONAccountsStore)(nil)

func NewJSONAccountsStore(dataDir string) *JSONAccountsStore {
	return &JSONAccountsStore{
		path: filepath.Join(dataDir, "accounts.json"),
	}
}

func (s *JSONAccountsStore) Load() (accounts.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return accounts.State{}, nil
		}
		return accounts.State{}, fmt.Errorf("read accounts store: %w", err)
	}

	var state accounts.State
	if err := json.Unmarshal(raw, &state); err != nil {
		return accounts.State{}, fmt.Errorf("decode accounts store: %w", err)
	}
	return state, nil
}

func (s *JSONAccountsStore) Save(state accounts.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create store dir: %w", err)
	}

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode accounts store: %w", err)
	}
	payload = append(payload, '\n')

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return fmt.Errorf("write tmp accounts store: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename accounts store: %w", err)
	}
	return nil
}
