package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
)

// Clone copies the current repository to .homura/<branch-name>/
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

	// Check if copy already exists
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("copy already exists at %s", destPath)
	}

	// Create .homura directory if it doesn't exist
	homuraDir := git.GetHomuraDir(repoRoot)
	if err := os.MkdirAll(homuraDir, 0755); err != nil {
		return fmt.Errorf("failed to create .homura directory: %w", err)
	}

	// Copy the repository recursively
	fmt.Printf("Copying repository to %s...\n", destPath)
	if err := copyDir(repoRoot, destPath); err != nil {
		return fmt.Errorf("failed to copy repository: %w", err)
	}

	// Checkout the branch in the copy
	fmt.Printf("Checking out branch '%s' in copy...\n", branchName)
	if err := git.CheckoutBranch(destPath, branchName); err != nil {
		return fmt.Errorf("failed to checkout branch: %w", err)
	}

	// Update state to set this as default branch
	state := &config.RepoState{
		DefaultBranch: branchName,
	}
	if err := config.SaveState(repoRoot, state); err != nil {
		return fmt.Errorf("failed to save state: %w", err)
	}

	fmt.Printf("Successfully cloned repository to .homura/%s/\n", branchName)
	fmt.Printf("Default branch set to '%s'\n", branchName)

	return nil
}

// copyDir recursively copies a directory tree
func copyDir(src, dst string) error {
	// Get source directory info
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	// Create destination directory
	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	// Read source directory
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		// Skip .homura directory to avoid recursive copy
		if entry.Name() == ".homura" && filepath.Dir(srcPath) == src {
			continue
		}

		if entry.IsDir() {
			// Recursively copy subdirectory
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			// Copy file
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file
func copyFile(src, dst string) error {
	// Get source file info
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	// Copy contents
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// Set file permissions
	if err := os.Chmod(dst, srcInfo.Mode()); err != nil {
		return err
	}

	return nil
}
