package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Safe devices to bind-mount from host (can't use mknod in user namespaces)
var safeDevices = []string{
	"null",
	"zero",
	"full",
	"random",
	"urandom",
	"tty",
}

// symlinkInfo describes a symlink to create in /dev
type symlinkInfo struct {
	path   string
	target string
}

// Standard symlinks for /dev
var devSymlinks = []symlinkInfo{
	{path: "stdin", target: "/proc/self/fd/0"},
	{path: "stdout", target: "/proc/self/fd/1"},
	{path: "stderr", target: "/proc/self/fd/2"},
	{path: "fd", target: "/proc/self/fd"},
	{path: "ptmx", target: "pts/ptmx"},
}

// SetupMinimalDev creates a minimal /dev with only safe devices
func SetupMinimalDev(devPath string) error {
	slog.Info("setting up minimal /dev", "path", devPath)

	// Mount tmpfs for /dev
	if err := unix.Mount("tmpfs", devPath, "tmpfs", unix.MS_NOSUID|unix.MS_STRICTATIME, "mode=755"); err != nil {
		return fmt.Errorf("failed to mount tmpfs on %s: %w", devPath, err)
	}

	// Create /dev/pts directory
	ptsPath := filepath.Join(devPath, "pts")
	if err := os.Mkdir(ptsPath, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", ptsPath, err)
	}

	// Mount devpts for pseudoterminal support
	if err := unix.Mount("devpts", ptsPath, "devpts", 0, "newinstance,ptmxmode=0666"); err != nil {
		return fmt.Errorf("failed to mount devpts on %s: %w", ptsPath, err)
	}

	// Create /dev/shm directory
	shmPath := filepath.Join(devPath, "shm")
	if err := os.Mkdir(shmPath, 01777); err != nil {
		return fmt.Errorf("failed to create %s: %w", shmPath, err)
	}

	// Mount tmpfs for /dev/shm
	if err := unix.Mount("tmpfs", shmPath, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=1777"); err != nil {
		return fmt.Errorf("failed to mount tmpfs on %s: %w", shmPath, err)
	}

	// Bind-mount safe devices from host
	// (mknod doesn't work in user namespaces - no CAP_MKNOD)
	for _, dev := range safeDevices {
		hostDev := filepath.Join("/dev", dev)
		targetDev := filepath.Join(devPath, dev)

		// Create empty file to mount over
		f, err := os.Create(targetDev)
		if err != nil {
			return fmt.Errorf("failed to create mount point for %s: %w", dev, err)
		}
		f.Close()

		// Bind mount the device from host
		if err := unix.Mount(hostDev, targetDev, "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("failed to bind mount %s: %w", dev, err)
		}
		slog.Debug("bind-mounted device", "path", targetDev)
	}

	// Create symlinks
	for _, link := range devSymlinks {
		linkPath := filepath.Join(devPath, link.path)
		if err := os.Symlink(link.target, linkPath); err != nil {
			return fmt.Errorf("failed to create symlink %s -> %s: %w", linkPath, link.target, err)
		}
		slog.Debug("created symlink", "path", linkPath, "target", link.target)
	}

	return nil
}
