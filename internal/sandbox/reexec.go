package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"

	"github.com/lun-4/homura/internal/fusefs"
)

// ReexecSelf starts FUSE server in parent, then re-executes child inside namespaces
func ReexecSelf(cfg *SandboxConfig) error {
	// Create temporary directory for FUSE mount point
	fuseMountPoint, err := os.MkdirTemp("", "homura-fuse-*")
	if err != nil {
		return fmt.Errorf("failed to create FUSE mount point: %w", err)
	}
	defer os.RemoveAll(fuseMountPoint)

	slog.Info("FUSE mount point created", "path", fuseMountPoint)

	// Start FUSE server in parent (privileged context)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Define allowed write paths
	allowedWritePaths := []string{
		cfg.WorktreePath, // The worktree directory
		"/tmp",           // Temporary files
		"/dev/shm",       // Shared memory
		"/dev",           // Device nodes (writes go to drivers, not disk)
	}

	fuseServer, err := fusefs.StartServer(ctx, fuseMountPoint, "/", allowedWritePaths)
	if err != nil {
		return fmt.Errorf("failed to start FUSE server: %w", err)
	}
	defer fuseServer.Unmount()

	slog.Info("FUSE server started in parent process", "allowedWritePaths", allowedWritePaths)

	// Update config with FUSE mount point
	cfg.FuseMountPoint = fuseMountPoint

	// Encode config for passing to child
	cfgData, err := cfg.Encode()
	if err != nil {
		return err
	}

	// Get the path to our own executable
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	slog.Info("re-executing self in namespaces", "executable", self)

	// Build the command to re-exec ourselves
	// We pass "sh" and the branch name so the child can continue the sh command flow
	cmd := exec.Command(self, "sh", "--sandbox", cfg.BranchName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set environment variables for the child
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("%s=%s", EnvReexec, ReexecInit),
		fmt.Sprintf("%s=%s", EnvConfig, cfgData),
	)

	// Set up the namespaces for the child process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: 0,
				HostID:      os.Getgid(),
				Size:        1,
			},
		},
	}

	// Run and wait for the child
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Propagate the exit code from the child
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus())
			}
		}
		return fmt.Errorf("sandbox child process failed: %w", err)
	}

	return nil
}
