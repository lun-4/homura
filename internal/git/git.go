package git

import (
	"fmt"
	"log/slog"
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

// GetRepoRoot returns the root directory of the git repository.
// If the current directory is inside a .homura/<branch>/ subdirectory,
// it returns the parent repository root instead, so that homura commands
// operate on the main repo rather than creating nested .homura directories.
func GetRepoRoot(startPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = startPath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", startPath)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository")
	}
	repoRoot := strings.TrimSpace(string(output))

	// Check if this repo root is inside a .homura/ subdirectory of a parent repo
	if parentRoot, ok := resolveHomuraParent(repoRoot); ok {
		slog.Info("detected homura subdirectory, using parent repo", "child", repoRoot, "parent", parentRoot)
		return parentRoot, nil
	}

	return repoRoot, nil
}

// resolveHomuraParent checks if the given path is inside a .homura/<branch>/
// directory and returns the parent repo root if so.
func resolveHomuraParent(repoRoot string) (string, bool) {
	cleanPath := filepath.Clean(repoRoot)
	sep := string(filepath.Separator)

	// Look for /.homura/ in the path
	homuraSegment := sep + ".homura" + sep
	idx := strings.LastIndex(cleanPath, homuraSegment)
	if idx == -1 {
		return "", false
	}

	parentRoot := cleanPath[:idx]
	if !IsGitRepo(parentRoot) {
		return "", false
	}

	return parentRoot, true
}

// HasUncommittedChanges checks if the repository has uncommitted changes
func HasUncommittedChanges(repoPath string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoPath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
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
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
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
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Branch doesn't exist, create it
	cmd = exec.Command("git", "checkout", "-b", branchName)
	cmd.Dir = repoPath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
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

// WorktreeAdd creates a new git worktree at the specified path with a new branch
func WorktreeAdd(repoPath, worktreePath, branchName string) error {
	cmd := exec.Command("git", "worktree", "add", "-b", branchName, worktreePath)
	cmd.Dir = repoPath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create worktree: %s: %w", string(output), err)
	}
	return nil
}

// WorktreeAddExisting creates a new git worktree for an existing branch
func WorktreeAddExisting(repoPath, worktreePath, branchName string) error {
	cmd := exec.Command("git", "worktree", "add", worktreePath, branchName)
	cmd.Dir = repoPath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create worktree: %s: %w", string(output), err)
	}
	return nil
}

// WorktreeRemove removes a git worktree
func WorktreeRemove(repoPath, worktreePath string, force bool) error {
	args := []string{"worktree", "remove", worktreePath}
	if force {
		args = append(args, "--force")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to remove worktree: %s: %w", string(output), err)
	}
	return nil
}

// WorktreeList returns a list of all worktrees
func WorktreeList(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %w", err)
	}

	var worktrees []string
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "worktree ") {
			worktrees = append(worktrees, strings.TrimPrefix(line, "worktree "))
		}
	}
	return worktrees, nil
}

// BranchExists checks if a branch exists
func BranchExists(repoPath, branchName string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", branchName)
	cmd.Dir = repoPath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", repoPath)
	return cmd.Run() == nil
}

// AddToGitExclude adds a pattern to $GIT_DIR/info/exclude for the given worktree.
// This uses the per-worktree exclude file so patterns don't affect other worktrees
// or get committed to the repository.
func AddToGitExclude(worktreePath, pattern string) error {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = worktreePath
	slog.Info("running git command", "cmd", "git", "args", cmd.Args[1:], "dir", worktreePath)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to find git dir: %w", err)
	}

	gitDir := strings.TrimSpace(string(output))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}

	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0755); err != nil {
		return fmt.Errorf("failed to create info directory: %w", err)
	}

	excludePath := filepath.Join(infoDir, "exclude")

	// Check if pattern already exists
	if existing, err := os.ReadFile(excludePath); err == nil {
		for _, line := range strings.Split(string(existing), "\n") {
			if line == pattern {
				return nil
			}
		}
	}

	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open exclude file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(pattern + "\n"); err != nil {
		return fmt.Errorf("failed to write to exclude file: %w", err)
	}

	return nil
}

// CreateClaudeLocalFile creates or appends to CLAUDE.local.md in a worktree
func CreateClaudeLocalFile(destPath, branchName string) error {
	claudeLocalPath := filepath.Join(destPath, "CLAUDE.local.md")

	content := `## Worktree Isolation

This is an isolated git worktree. Stay within this directory - do not access the parent repository unless explicitly asked. If you need files outside this worktree, ask the user first.
`

	f, err := os.OpenFile(claudeLocalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open CLAUDE.local.md: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(content); err != nil {
		return fmt.Errorf("failed to write to CLAUDE.local.md: %w", err)
	}

	return nil
}
