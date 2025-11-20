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

// migration represents a database schema migration
type migration struct {
	version int
	name    string
	sql     string
}

// migrations is the list of all database migrations in order
var migrations = []migration{
	{
		version: 1,
		name:    "create_state_table",
		sql: `
		CREATE TABLE IF NOT EXISTS state (
			key TEXT PRIMARY KEY,
			value TEXT
		);
		`,
	},
}

// initDB initializes the database and runs any pending migrations
func initDB(db *sql.DB) error {
	// Create migration_state table if it doesn't exist
	createMigrationTable := `
	CREATE TABLE IF NOT EXISTS migration_state (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	slog.Info("creating migration_state table")
	_, err := db.Exec(createMigrationTable)
	if err != nil {
		return fmt.Errorf("failed to create migration_state table: %w", err)
	}

	// Run pending migrations
	for _, m := range migrations {
		// Check if migration has already been applied
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM migration_state WHERE version = ?", m.version).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to check migration status: %w", err)
		}

		if count > 0 {
			slog.Info("migration already applied", "version", m.version, "name", m.name)
			continue
		}

		// Run the migration
		slog.Info("applying migration", "version", m.version, "name", m.name)
		_, err = db.Exec(m.sql)
		if err != nil {
			return fmt.Errorf("failed to apply migration %d (%s): %w", m.version, m.name, err)
		}

		// Record that the migration was applied
		_, err = db.Exec("INSERT INTO migration_state (version, name) VALUES (?, ?)", m.version, m.name)
		if err != nil {
			return fmt.Errorf("failed to record migration: %w", err)
		}

		slog.Info("migration applied successfully", "version", m.version, "name", m.name)
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

	// Initialize schema and run migrations
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

	// Initialize schema and run migrations
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
