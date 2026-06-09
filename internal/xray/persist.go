package xray

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const persistedStartFile = "last-start.json"

func savePersistedStart(dataDir string, req StartRequest) error {
	if dataDir == "" {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal start request: %w", err)
	}

	target := filepath.Join(dataDir, persistedStartFile)
	temp := target + ".tmp"
	if err := os.WriteFile(temp, payload, 0o600); err != nil {
		return fmt.Errorf("write temp start state: %w", err)
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("rename start state: %w", err)
	}
	return nil
}

func loadPersistedStart(dataDir string) (*StartRequest, error) {
	if dataDir == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, persistedStartFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read start state: %w", err)
	}
	var req StartRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("parse start state: %w", err)
	}
	if len(req.XrayConfig) == 0 {
		return nil, nil
	}
	req.Internals.ForceRestart = false
	return &req, nil
}

func clearPersistedStart(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	err := os.Remove(filepath.Join(dataDir, persistedStartFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
