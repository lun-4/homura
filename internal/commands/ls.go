package commands

import (
	"fmt"
	"os"

	"github.com/yourusername/homura/internal/config"
	"github.com/yourusername/homura/internal/git"
)

// Ls lists all branch copies
func Ls() error {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Validate we're in a git repo
	repoRoot, err := git.GetRepoRoot(cwd)
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	// Get .homura directory
	homuraDir := git.GetHomuraDir(repoRoot)

	// Check if .homura directory exists
	if _, err := os.Stat(homuraDir); os.IsNotExist(err) {
		fmt.Println("No copies found")
		return nil
	}

	// Load state to get default branch
	state, err := config.LoadState(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	// Read .homura directory
	entries, err := os.ReadDir(homuraDir)
	if err != nil {
		return fmt.Errorf("failed to read .homura directory: %w", err)
	}

	// Filter out non-directory entries and state.toml
	var copies []string
	for _, entry := range entries {
		if entry.IsDir() {
			copies = append(copies, entry.Name())
		}
	}

	if len(copies) == 0 {
		fmt.Println("No copies found")
		return nil
	}

	// Print copies
	fmt.Println("Branch copies:")
	for _, copy := range copies {
		if copy == state.DefaultBranch {
			fmt.Printf("  %s (default)\n", copy)
		} else {
			fmt.Printf("  %s\n", copy)
		}
	}

	return nil
}
