package commands

import (
	"fmt"
	"os"

	"github.com/yourusername/homura/internal/config"
	"github.com/yourusername/homura/internal/git"
)

// Rm removes a branch copy
func Rm(branchName string, force bool) error {
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

	// If no branch name provided, use default branch
	if branchName == "" {
		state, err := config.LoadState(repoRoot)
		if err != nil {
			return fmt.Errorf("failed to load state: %w", err)
		}
		if state.DefaultBranch == "" {
			return fmt.Errorf("no default branch set and no branch name provided")
		}
		branchName = state.DefaultBranch
	}

	// Get copy path
	copyPath := git.GetCopyPath(repoRoot, branchName)

	// Check if copy exists
	if _, err := os.Stat(copyPath); os.IsNotExist(err) {
		return fmt.Errorf("copy does not exist at %s", copyPath)
	}

	// Check for uncommitted changes
	hasChanges, err := git.HasUncommittedChanges(copyPath)
	if err != nil {
		return fmt.Errorf("failed to check for uncommitted changes: %w", err)
	}

	if hasChanges && !force {
		// Show git status
		status, err := git.GetStatus(copyPath)
		if err != nil {
			return fmt.Errorf("failed to get git status: %w", err)
		}

		fmt.Println("Copy has uncommitted changes:")
		fmt.Println(status)
		fmt.Println("\nUse -f flag to force removal")
		return fmt.Errorf("refusing to remove copy with uncommitted changes")
	}

	// Remove the copy directory
	fmt.Printf("Removing .homura/%s/...\n", branchName)
	if err := os.RemoveAll(copyPath); err != nil {
		return fmt.Errorf("failed to remove copy: %w", err)
	}

	fmt.Printf("Successfully removed .homura/%s/\n", branchName)

	return nil
}
