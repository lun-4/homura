package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
)

// Rm removes a worktree
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

	// Get worktree path
	worktreePath := git.GetCopyPath(repoRoot, branchName)
	if filepath.Dir(worktreePath) != git.GetHomuraDir(repoRoot) {
		return fmt.Errorf("invalid branch name %q", branchName)
	}

	// Check if worktree exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return fmt.Errorf("worktree does not exist at %s", worktreePath)
	}

	info, err := os.Lstat(worktreePath)
	if err != nil {
		return fmt.Errorf("failed to inspect copy: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to remove non-directory copy at %s", worktreePath)
	}
	if git.IsGitRepo(worktreePath) && !force {
		return fmt.Errorf("independent repository copies may contain unique commits and local files; use -f to remove %s", worktreePath)
	}

	// Check for uncommitted changes (unless force is set)
	if !force {
		hasChanges, err := git.HasUncommittedChanges(worktreePath)
		if err != nil {
			return fmt.Errorf("failed to check for uncommitted changes: %w", err)
		}

		if hasChanges {
			// Show git status
			status, err := git.GetStatus(worktreePath)
			if err != nil {
				return fmt.Errorf("failed to get git status: %w", err)
			}

			fmt.Println("Worktree has uncommitted changes:")
			fmt.Println(status)
			fmt.Println("\nUse -f flag to force removal")
			return fmt.Errorf("refusing to remove worktree with uncommitted changes")
		}
	}

	// Remove the worktree using git worktree remove
	fmt.Printf("Removing worktree .homura/%s/...\n", git.BranchDirName(branchName))
	if git.IsGitRepo(worktreePath) {
		if err := os.RemoveAll(worktreePath); err != nil {
			return fmt.Errorf("failed to remove repository copy: %w", err)
		}
	} else if err := git.WorktreeRemove(repoRoot, worktreePath, force); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	fmt.Printf("Successfully removed worktree .homura/%s/\n", git.BranchDirName(branchName))

	return nil
}
