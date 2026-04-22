package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const cacheFilename = "models-cache.json"

func LoadCache(dataDir string) (CacheSnapshot, error) {
	path := filepath.Join(dataDir, cacheFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CacheSnapshot{}, nil
		}
		return CacheSnapshot{}, fmt.Errorf("read models cache: %w", err)
	}

	var snapshot CacheSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return CacheSnapshot{}, fmt.Errorf("decode models cache: %w", err)
	}
	return snapshot, nil
}

func SaveCache(dataDir string, snapshot CacheSnapshot) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create models cache dir: %w", err)
	}

	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode models cache: %w", err)
	}
	payload = append(payload, '\n')

	path := filepath.Join(dataDir, cacheFilename)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return fmt.Errorf("write tmp models cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename models cache: %w", err)
	}
	return nil
}
