package sandbox

import (
	"fmt"
	"log/slog"

	"golang.org/x/sys/unix"
)

// MakeMountsPrivate makes all mounts private so changes don't propagate to the parent namespace
func MakeMountsPrivate() error {
	slog.Info("making all mounts private")

	// Make the root mount private recursively
	// This prevents mount events from propagating to/from the parent namespace
	if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("failed to make mounts private: %w", err)
	}

	return nil
}

// MountProc mounts a fresh procfs at the specified path
func MountProc(target string) error {
	slog.Info("mounting procfs", "target", target)

	if err := unix.Mount("proc", target, "proc", 0, ""); err != nil {
		return fmt.Errorf("failed to mount proc at %s: %w", target, err)
	}

	return nil
}

// MountSys bind-mounts /sys at the specified path
// Note: user namespaces can't add read-only restrictions to bind mounts
func MountSys(target string) error {
	slog.Info("mounting sysfs", "target", target)

	if err := unix.Mount("/sys", target, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("failed to bind mount sys at %s: %w", target, err)
	}

	return nil
}

// MountTmpfs mounts a tmpfs at the specified path
func MountTmpfs(target string) error {
	slog.Info("mounting tmpfs", "target", target)

	if err := unix.Mount("tmpfs", target, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=755"); err != nil {
		return fmt.Errorf("failed to mount tmpfs at %s: %w", target, err)
	}

	return nil
}

// BindMountSelf bind-mounts a directory to itself
// This is useful to make a directory a proper mount point for chroot
func BindMountSelf(target string) error {
	slog.Info("bind mounting to self", "target", target)

	if err := unix.Mount(target, target, "", unix.MS_BIND|unix.MS_REC, ""); err != nil {
		return fmt.Errorf("failed to bind mount %s to itself: %w", target, err)
	}

	return nil
}
