package commands

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
)

// Sh opens a shell in the specified branch copy
func Sh(branchName string) error {
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

	// Get shell from environment or use /bin/sh
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	fmt.Printf("Opening shell in .homura/%s/\n", branchName)
	fmt.Printf("Type 'exit' to return to original repository\n")

	// Change to copy directory and spawn shell
	cmd := exec.Command(shell)
	cmd.Dir = copyPath
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the shell
	if err := cmd.Run(); err != nil {
		// Check if this is an exit status error (which is normal when user exits shell)
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				// Exit code 0 or 130 (Ctrl+D) are normal
				if status.ExitStatus() == 0 || status.ExitStatus() == 130 {
					return nil
				}
			}
		}
		return fmt.Errorf("failed to run shell: %w", err)
	}

	return nil
}
