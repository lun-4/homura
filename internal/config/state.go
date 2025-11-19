package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// RepoState tracks per-repository state
type RepoState struct {
	DefaultBranch string `toml:"default_branch"`
}

// GetStateFilePath returns the path to the state file for the current repo
func GetStateFilePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".homura", "state.toml")
}

// LoadState loads the state file for the current repo
func LoadState(repoRoot string) (*RepoState, error) {
	statePath := GetStateFilePath(repoRoot)

	// If state file doesn't exist, return empty state
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		return &RepoState{}, nil
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state RepoState
	if err := toml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	return &state, nil
}

// SaveState saves the state file for the current repo
func SaveState(repoRoot string, state *RepoState) error {
	statePath := GetStateFilePath(repoRoot)

	// Ensure .homura directory exists
	homuraDir := filepath.Dir(statePath)
	if err := os.MkdirAll(homuraDir, 0755); err != nil {
		return fmt.Errorf("failed to create .homura directory: %w", err)
	}

	data, err := toml.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}
