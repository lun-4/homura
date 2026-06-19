package commands

import (
	"fmt"
	"os"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
)

// Clone creates a new git worktree in .homura/<branch-name>/.
// If base is non-empty and a new branch is being created, the branch forks from
// that commit-ish instead of the repository's current HEAD. base is only valid
// when creating a new branch.
func Clone(branchName, base string) error {
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
		if base != "" {
			return fmt.Errorf("branch %q already exists; the base argument only applies when creating a new branch", branchName)
		}
		// Use existing branch
		fmt.Printf("Creating worktree for existing branch '%s' at %s...\n", branchName, destPath)
		if err := git.WorktreeAddExisting(repoRoot, destPath, branchName); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}
	} else {
		// Create new branch with worktree
		if base != "" {
			fmt.Printf("Creating worktree with new branch '%s' from '%s' at %s...\n", branchName, base, destPath)
		} else {
			fmt.Printf("Creating worktree with new branch '%s' at %s...\n", branchName, destPath)
		}
		if err := git.WorktreeAdd(repoRoot, destPath, branchName, base); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}
	}

	// Create CLAUDE.local.md in the worktree
	if err := git.CreateClaudeLocalFile(destPath, branchName); err != nil {
		// Non-fatal warning - don't block the clone
		fmt.Fprintf(os.Stderr, "Warning: failed to create CLAUDE.local.md: %v\n", err)
	}

	// Add CLAUDE.local.md to the worktree's git exclude so it doesn't show as untracked
	if err := git.AddToGitExclude(destPath, "CLAUDE.local.md"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to add CLAUDE.local.md to git exclude: %v\n", err)
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
