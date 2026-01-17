package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// RunChild is the entry point for the sandboxed child process
// The FUSE server is already running in the parent process
func RunChild() error {
	slog.Info("sandbox child starting")

	// Decode config from environment
	cfg, err := DecodeConfig()
	if err != nil {
		return err
	}

	fuseMountPoint := cfg.FuseMountPoint

	slog.Info("sandbox config loaded",
		"worktree", cfg.WorktreePath,
		"branch", cfg.BranchName,
		"command", cfg.Command,
		"args", cfg.Args,
		"fuseMountPoint", fuseMountPoint,
	)

	// Make all mounts private so our changes don't affect the parent namespace
	if err := MakeMountsPrivate(); err != nil {
		return err
	}

	// FUSE server is running in parent, we just use the mount

	// Create pivot_old directory BEFORE bind mount (while FUSE filtering is active)
	pivotOldPath := filepath.Join(fuseMountPoint, "tmp", ".pivot_old")
	if err := os.MkdirAll(pivotOldPath, 0700); err != nil {
		return fmt.Errorf("failed to create pivot_old dir: %w", err)
	}

	// Bind mount the FUSE root to itself to make it a proper mount point
	if err := BindMountSelf(fuseMountPoint); err != nil {
		return err
	}

	// Mount fresh procfs (for new PID namespace)
	procPath := filepath.Join(fuseMountPoint, "proc")
	if err := MountProc(procPath); err != nil {
		return err
	}

	// Mount minimal /dev with bind-mounted devices (FUSE can't proxy device files)
	devPath := filepath.Join(fuseMountPoint, "dev")
	if err := SetupMinimalDev(devPath); err != nil {
		return err
	}

	// Mount /sys
	sysPath := filepath.Join(fuseMountPoint, "sys")
	if err := MountSys(sysPath); err != nil {
		return err
	}

	// Use pivot_root to change the root filesystem
	if err := PivotRoot(fuseMountPoint); err != nil {
		return err
	}

	// Change to the worktree directory (same absolute path, now through FUSE)
	if err := os.Chdir(cfg.WorktreePath); err != nil {
		return fmt.Errorf("failed to chdir to worktree: %w", err)
	}

	slog.Info("spawning command", "command", cfg.Command, "args", cfg.Args, "cwd", cfg.WorktreePath)

	// Spawn the command
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				// Propagate the exit code
				os.Exit(status.ExitStatus())
			}
		}
		return fmt.Errorf("command execution failed: %w", err)
	}

	return nil
}
