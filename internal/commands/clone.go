package commands

import (
	"fmt"
	"os"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
)

// Clone creates a new git worktree in .homura/<branch-name>/
func Clone(branchName string) error {
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

	// Get destination path
	destPath := git.GetCopyPath(repoRoot, branchName)

	// Check if worktree already exists
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("worktree already exists at %s", destPath)
	}

	// Create .homura directory if it doesn't exist
	homuraDir := git.GetHomuraDir(repoRoot)
	if err := os.MkdirAll(homuraDir, 0755); err != nil {
		return fmt.Errorf("failed to create .homura directory: %w", err)
	}

	// Check if branch already exists
	if git.BranchExists(repoRoot, branchName) {
		// Use existing branch
		fmt.Printf("Creating worktree for existing branch '%s' at %s...\n", branchName, destPath)
		if err := git.WorktreeAddExisting(repoRoot, destPath, branchName); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}
	} else {
		// Create new branch with worktree
		fmt.Printf("Creating worktree with new branch '%s' at %s...\n", branchName, destPath)
		if err := git.WorktreeAdd(repoRoot, destPath, branchName); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}
	}

	// Update state to set this as default branch
	state := &config.RepoState{
		DefaultBranch: branchName,
	}
	if err := config.SaveState(repoRoot, state); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	fmt.Printf("Successfully created worktree at .homura/%s/\n", branchName)
	fmt.Printf("Default branch set to '%s'\n", branchName)

	return nil
}
