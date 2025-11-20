package config

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// RepoState tracks per-repository state
type RepoState struct {
	DefaultBranch string
}

// GetStateFilePath returns the path to the state database for the current repo
func GetStateFilePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".homura", "state.db")
}

// initDB initializes the database schema if it doesn't exist
func initDB(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS state (
		key TEXT PRIMARY KEY,
		value TEXT
	);
	`
	slog.Info("initializing database schema")
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}
	return nil
}

// LoadState loads the state from the database for the current repo
func LoadState(repoRoot string) (*RepoState, error) {
	statePath := GetStateFilePath(repoRoot)

	// If state file doesn't exist, return empty state
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		return &RepoState{}, nil
	}

	slog.Info("opening state database", "path", statePath)
	db, err := sql.Open("sqlite3", statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	// Initialize schema
	if err := initDB(db); err != nil {
		return nil, err
	}

	// Query default branch
	var defaultBranch string
	err = db.QueryRow("SELECT value FROM state WHERE key = ?", "default_branch").Scan(&defaultBranch)
	if err == sql.ErrNoRows {
		return &RepoState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query default branch: %w", err)
	}

	slog.Info("loaded state from database", "default_branch", defaultBranch)
	return &RepoState{DefaultBranch: defaultBranch}, nil
}

// SaveState saves the state to the database for the current repo
func SaveState(repoRoot string, state *RepoState) error {
	statePath := GetStateFilePath(repoRoot)

	// Ensure .homura directory exists
	homuraDir := filepath.Dir(statePath)
	if err := os.MkdirAll(homuraDir, 0755); err != nil {
		return fmt.Errorf("failed to create .homura directory: %w", err)
	}

	slog.Info("opening state database", "path", statePath)
	db, err := sql.Open("sqlite3", statePath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer db.Close()

	// Initialize schema
	if err := initDB(db); err != nil {
		return err
	}

	// Insert or replace default branch
	slog.Info("saving state to database", "default_branch", state.DefaultBranch)
	_, err = db.Exec("INSERT OR REPLACE INTO state (key, value) VALUES (?, ?)", "default_branch", state.DefaultBranch)
	if err != nil {
		return fmt.Errorf("failed to save default branch: %w", err)
	}

	return nil
}
