package commands

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
	"github.com/lun-4/homura/internal/sandbox"
)

// Exec runs a command in the specified branch copy
func Exec(branchName string, sandboxed bool, cmdArgs []string) error {
	// Check if we're the re-exec'd child inside the sandbox
	if sandbox.IsReexecChild() {
		return sandbox.RunChild()
	}

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

	if len(cmdArgs) == 0 {
		return fmt.Errorf("no command provided")
	}

	// If sandboxed mode, re-exec with namespaces
	if sandboxed {
		cfg := &sandbox.SandboxConfig{
			WorktreePath: copyPath,
			BranchName:   branchName,
			Command:      cmdArgs[0],
			Args:         cmdArgs[1:],
			RepoRoot:     repoRoot,
		}
		return sandbox.ReexecSelf(cfg)
	}

	// Execute command in copy directory
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = copyPath
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the command
	if err := cmd.Run(); err != nil {
		// Check if this is an exit status error
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				// Return the actual exit code
				os.Exit(status.ExitStatus())
			}
		}
		return fmt.Errorf("failed to run command: %w", err)
	}

	return nil
}
