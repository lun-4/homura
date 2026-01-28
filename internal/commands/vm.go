package commands

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
	"github.com/lun-4/homura/internal/vm"
	"github.com/spf13/cobra"
)

// RunVM implements the `homura vm` command
func RunVM(cmd *cobra.Command, args []string, branchName string) error {
	slog.Info("Starting homura VM")

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
		return fmt.Errorf("copy does not exist at %s\nRun 'homura clone %s' first", copyPath, branchName)
	}

	// Change to the copy directory
	if err := os.Chdir(copyPath); err != nil {
		return fmt.Errorf("failed to change to copy directory: %w", err)
	}

	slog.Info("Running VM in copy", "branch", branchName, "path", copyPath)

	// Create example custom Dockerfile if config directory doesn't exist
	configDir := filepath.Join(os.Getenv("HOME"), ".config", "homura")
	examplePath := filepath.Join(configDir, "Dockerfile.custom.example")

	if _, err := os.Stat(examplePath); os.IsNotExist(err) {
		os.MkdirAll(configDir, 0755)

		exampleContent := fmt.Sprintf(`# homura VM Custom Dockerfile
# This file extends the base VM image with your customizations
# Copy to Dockerfile.custom and edit to apply changes
#
# IMPORTANT: The FROM line must match the current homura version
# When homura updates, you must update this FROM line to match

FROM homura-vm-alpine-base:v%d

# Example: Add additional packages
RUN apk add --no-cache \
    vim \
    neovim \
    tmux \
    ripgrep \
    fd \
    bat

# Example: Install Python packages
RUN pip install --break-system-packages \
    anthropic \
    requests \
    numpy

# Example: Set environment variables
ENV MY_CUSTOM_VAR=value

# Example: Add custom PATH entries
RUN echo 'export PATH=$PATH:/custom/bin' >> /root/.profile

# Example: Configure shell (fish is default)
RUN echo 'set -gx MY_VAR value' >> /root/.config/fish/config.fish

# Note: You cannot COPY files from host in this Dockerfile
# Use 9p mounts instead: homura 9p expose /path/to/files
`, vm.VMImplementationVersion)

		if err := os.WriteFile(examplePath, []byte(exampleContent), 0644); err != nil {
			slog.Warn("Failed to create example Dockerfile", "error", err)
		} else {
			slog.Info("Created example custom Dockerfile", "path", examplePath)
		}
	}

	// Create new VM instance
	vmInstance, err := vm.NewVM()
	if err != nil {
		return fmt.Errorf("failed to create VM: %w", err)
	}

	// Ensure cleanup happens on exit
	defer func() {
		if err := vmInstance.Cleanup(); err != nil {
			slog.Error("Failed to cleanup VM state", "error", err)
		}
	}()

	// Start the VM
	if err := vmInstance.Start(); err != nil {
		return fmt.Errorf("failed to start VM: %w", err)
	}

	// Wait for VM to exit
	if err := vmInstance.Wait(); err != nil {
		slog.Debug("VM wait returned", "error", err)
	}

	slog.Info("VM shutdown complete")
	return nil
}

// VMSsh implements the `homura vm ssh` command
func VMSsh(cmd *cobra.Command, args []string) error {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Find the running VM for this working directory
	slot, err := vm.FindVMByWorkDir(cwd)
	if err != nil {
		return fmt.Errorf("failed to find VM: %w", err)
	}

	slog.Info("Connecting to VM", "ip", slot.IPAddress, "ssh_port", slot.PortStart, "working_dir", cwd)

	// Use syscall.Exec to replace the current process with SSH
	// This gives the user a clean SSH session
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh command not found: %w", err)
	}

	// SSH is mapped to the first port in the slot's range
	sshArgs := []string{"ssh", "-p", fmt.Sprintf("%d", slot.PortStart), fmt.Sprintf("root@%s", slot.IPAddress)}

	// Replace current process with SSH
	if err := syscall.Exec(sshPath, sshArgs, os.Environ()); err != nil {
		return fmt.Errorf("failed to exec ssh: %w", err)
	}

	// This line is unreachable if exec succeeds
	return nil
}
