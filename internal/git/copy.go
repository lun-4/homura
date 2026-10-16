package git

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func CopyRepository(repoPath, destPath, branchName, base string) (err error) {
	run := func(dir string, args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	validated, err := run(repoPath, "check-ref-format", "--branch", branchName)
	if err != nil {
		return err
	}
	if validated != branchName {
		return fmt.Errorf("branch must be a literal name: %q", branchName)
	}
	gitDir := filepath.Join(repoPath, ".git")
	info, err := os.Lstat(gitDir)
	if err != nil {
		return fmt.Errorf("inspect .git: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source .git must be a physical directory")
	}
	if err := filepath.WalkDir(gitDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in git metadata: %s", path)
		}
		return nil
	}); err != nil {
		return err
	}
	configCmd := exec.Command("git", "config", "--get", "core.worktree")
	configCmd.Dir = repoPath
	if output, err := configCmd.CombinedOutput(); err == nil {
		return fmt.Errorf("source has core.worktree configured")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("inspect core.worktree: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	bare, err := run(repoPath, "rev-parse", "--is-bare-repository")
	if err != nil {
		return err
	}
	if bare != "false" {
		return fmt.Errorf("source must not be a bare repository")
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "objects", "info", "alternates")); !os.IsNotExist(err) {
		if err != nil {
			return fmt.Errorf("inspect object alternates: %w", err)
		}
		return fmt.Errorf("source uses object alternates")
	}
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
	cmd.Dir = repoPath
	exists := true
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return fmt.Errorf("check branch: %w", err)
		}
		exists = false
	}
	if exists && base != "" {
		return fmt.Errorf("branch %q already exists; cannot use a base", branchName)
	}
	if base != "" {
		base, err = run(repoPath, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
		if err != nil {
			return err
		}
	}
	source, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		return err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destPath))
	if err != nil {
		return err
	}
	destination, err := filepath.Abs(filepath.Join(parent, filepath.Base(destPath)))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(source, destination)
	if err != nil {
		return err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !strings.HasPrefix(rel, ".homura"+string(os.PathSeparator))) {
		return fmt.Errorf("destination must be outside the source or inside its .homura directory")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer func() {
		if err != nil {
			_ = filepath.WalkDir(destination, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr == nil && entry.IsDir() {
					return os.Chmod(path, 0700)
				}
				return walkErr
			})
			if cleanupErr := os.RemoveAll(destination); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove failed copy: %w", cleanupErr))
			}
		}
	}()
	if err = copyRepositoryEntry(source, destination, ""); err != nil {
		return err
	}
	args := []string{"checkout", "--no-guess", "--no-overwrite-ignore"}
	if !exists {
		args = append(args, "-b")
	}
	args = append(args, branchName)
	if base != "" {
		args = append(args, base)
	}
	args = append(args, "--")
	_, err = run(destination, args...)
	return err
}

func copyRepositoryEntry(source, destination, relative string) error {
	if relative != ".git" && filepath.Base(relative) == ".git" {
		return fmt.Errorf("nested git metadata is unsupported: %s", source)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	mode := info.Mode()
	switch {
	case mode.IsDir():
		if relative != "" {
			if err := os.Mkdir(destination, 0700); err != nil {
				return err
			}
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			rel := filepath.Join(relative, entry.Name())
			if rel == ".homura" || rel == filepath.Join(".git", "worktrees") {
				continue
			}
			if err := copyRepositoryEntry(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()), rel); err != nil {
				return err
			}
		}
		return os.Chmod(destination, mode)
	case mode.IsRegular():
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		return os.Chmod(destination, mode)
	case mode&os.ModeSymlink != 0:
		if strings.HasPrefix(relative, ".git"+string(os.PathSeparator)) {
			return fmt.Errorf("symlink in git metadata: %s", source)
		}
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(target, destination)
	default:
		return fmt.Errorf("unsupported file type: %s (%s)", source, mode)
	}
}
