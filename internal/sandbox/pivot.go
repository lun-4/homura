package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Chroot changes the root filesystem to newRoot using chroot
// This is simpler than pivot_root and avoids FUSE recursion issues
func Chroot(newRoot string) error {
	slog.Info("chrooting", "newRoot", newRoot)

	// Chroot to the new root
	if err := unix.Chroot(newRoot); err != nil {
		return fmt.Errorf("chroot failed: %w", err)
	}

	// Change to the new root
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("failed to chdir to new root: %w", err)
	}

	return nil
}

// PivotRoot uses pivot_root to change the root filesystem
// This properly moves all mounts to the new root
func PivotRoot(newRoot string) error {
	slog.Info("pivot_root", "newRoot", newRoot)

	// Create pivot_old directory in /tmp (which is writable through FUSE filter)
	pivotOld := filepath.Join(newRoot, "tmp", ".pivot_old")
	if err := os.MkdirAll(pivotOld, 0700); err != nil {
		return fmt.Errorf("failed to create pivot_old dir: %w", err)
	}

	// Change to new root before pivot
	if err := unix.Chdir(newRoot); err != nil {
		return fmt.Errorf("failed to chdir to new root: %w", err)
	}

	// pivot_root swaps the root filesystem
	// newRoot becomes /, old root moves to pivotOld
	if err := unix.PivotRoot(".", filepath.Join("tmp", ".pivot_old")); err != nil {
		return fmt.Errorf("pivot_root failed: %w", err)
	}

	// Now we're in the new root, chdir to /
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("failed to chdir to / after pivot: %w", err)
	}

	// Unmount and remove old root
	if err := unix.Unmount("/tmp/.pivot_old", unix.MNT_DETACH); err != nil {
		slog.Warn("failed to unmount old root", "error", err)
		// Continue anyway - it will be cleaned up when namespace exits
	}

	// Try to remove the pivot_old directory
	os.Remove("/tmp/.pivot_old")

	return nil
}
