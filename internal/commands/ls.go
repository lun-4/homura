package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
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

	// List worktrees and keep only those under .homura/
	worktrees, err := git.WorktreeList(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to list worktrees: %w", err)
	}

	homuraPrefix := homuraDir + string(os.PathSeparator)
	var copies []git.Worktree
	for _, wt := range worktrees {
		if strings.HasPrefix(wt.Path, homuraPrefix) {
			copies = append(copies, wt)
		}
	}

	if len(copies) == 0 {
		fmt.Println("No copies found")
		return nil
	}

	// Print copies
	fmt.Println("Branch copies:")
	for _, copy := range copies {
		name := copy.Branch
		if name == "" {
			name = filepath.Base(copy.Path) + " (detached)"
		}
		if copy.Branch == state.DefaultBranch {
			fmt.Printf("  %s (default)\n", name)
		} else {
			fmt.Printf("  %s\n", name)
		}
	}

	return nil
}
