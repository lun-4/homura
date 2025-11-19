package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsGitRepo checks if the current directory is a git repository
func IsGitRepo(path string) bool {
	gitDir := filepath.Join(path, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// GetRepoRoot returns the root directory of the git repository
func GetRepoRoot(startPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = startPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository")
	}
	return strings.TrimSpace(string(output)), nil
}

// HasUncommittedChanges checks if the repository has uncommitted changes
func HasUncommittedChanges(repoPath string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to check git status: %w", err)
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// GetStatus returns the git status output
func GetStatus(repoPath string) (string, error) {
	cmd := exec.Command("git", "status")
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get git status: %w", err)
	}
	return string(output), nil
}

// CheckoutBranch checks out a branch (creates it if it doesn't exist)
func CheckoutBranch(repoPath, branchName string) error {
	// Try to checkout existing branch first
	cmd := exec.Command("git", "checkout", branchName)
	cmd.Dir = repoPath
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Branch doesn't exist, create it
	cmd = exec.Command("git", "checkout", "-b", branchName)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create and checkout branch %s: %w", branchName, err)
	}

	return nil
}

// GetHomuraDir returns the .homura directory path for a repo
func GetHomuraDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".homura")
}

// GetCopyPath returns the path for a specific branch copy
func GetCopyPath(repoRoot, branchName string) string {
	return filepath.Join(GetHomuraDir(repoRoot), branchName)
}
