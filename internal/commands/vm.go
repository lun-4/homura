package commands

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lun-4/homura/internal/config"
	"github.com/lun-4/homura/internal/git"
	"github.com/lun-4/homura/internal/vm"
	"github.com/spf13/cobra"
)

// formatRelativeTime formats a Unix millisecond timestamp as relative time
func formatRelativeTime(createdAtMs int64) string {
	created := time.UnixMilli(createdAtMs)
	dur := time.Since(created)

	if dur < time.Minute {
		secs := int(dur.Seconds())
		if secs == 1 {
			return "1 second ago"
		}
		return fmt.Sprintf("%d seconds ago", secs)
	} else if dur < time.Hour {
		mins := int(dur.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	} else if dur < 24*time.Hour {
		hours := int(dur.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else {
		days := int(dur.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// RunVM implements the `homura vm` command
func RunVM(cmd *cobra.Command, args []string, branchName string, shareModeStr string) error {
	// Parse and validate share mode
	shareMode, err := vm.ParseShareMode(shareModeStr)
	if err != nil {
		return err
	}

	slog.Info("Starting homura VM", "share_mode", shareMode)

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

FROM homura-vm-ubuntu-base:v%d

# Example: Add additional packages
RUN apt-get update && apt-get install -y --no-install-recommends \
    vim \
    neovim \
    tmux \
    ripgrep \
    fd-find \
    bat \
    && rm -rf /var/lib/apt/lists/*

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
	vmInstance, err := vm.NewVM(shareMode)
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

// VMLs implements the `homura vm ls` command
func VMLs() error {
	slots, err := vm.ListAllVMs()
	if err != nil {
		return fmt.Errorf("failed to list VMs: %w", err)
	}

	if len(slots) == 0 {
		fmt.Println("No running VMs.")
		return nil
	}

	fmt.Printf("%-6s %-16s %-8s %-14s %-10s %-8s %s\n",
		"SLOT", "IP", "SSH", "PORTS", "SHARE", "PID", "WORKING DIR")

	for _, s := range slots {
		sshPort := fmt.Sprintf("%d", s.PortStart)
		portRange := fmt.Sprintf("%d-%d", s.PortStart, s.PortEnd)
		uptime := formatRelativeTime(s.CreatedAt)

		fmt.Printf("%-6d %-16s %-8s %-14s %-10s %-8d %s\n",
			s.SlotNumber, s.IPAddress, sshPort, portRange,
			s.ShareMode, s.VMPID, s.WorkingDir)
		fmt.Printf("       started %s\n", uptime)
	}

	fmt.Printf("\n%d VM(s) running\n", len(slots))
	return nil
}

// VMSsh implements the `homura vm ssh` command
func VMSsh(cmd *cobra.Command, args []string, branchName string) error {
	var workDir string

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	if branchName != "" {
		// Branch name provided - resolve to copy path
		// Validate we're in a git repo
		repoRoot, err := git.GetRepoRoot(cwd)
		if err != nil {
			return fmt.Errorf("not a git repository: %w", err)
		}

		// Get copy path for the specified branch
		copyPath := git.GetCopyPath(repoRoot, branchName)

		// Check if copy exists
		if _, err := os.Stat(copyPath); os.IsNotExist(err) {
			return fmt.Errorf("copy does not exist at %s\nRun 'homura clone %s' first", copyPath, branchName)
		}

		workDir = copyPath
	} else {
		// No branch provided - try default branch first, then fall back to cwd
		repoRoot, err := git.GetRepoRoot(cwd)
		if err == nil {
			// We're in a git repo, check for default branch
			state, err := config.LoadState(repoRoot)
			if err == nil && state.DefaultBranch != "" {
				// Default branch is set, use it
				copyPath := git.GetCopyPath(repoRoot, state.DefaultBranch)
				if _, err := os.Stat(copyPath); err == nil {
					// Copy exists, use it
					workDir = copyPath
					slog.Debug("Using default branch", "branch", state.DefaultBranch)
				} else {
					// Copy doesn't exist, fall back to cwd
					workDir = cwd
					slog.Debug("Default branch copy doesn't exist, using cwd")
				}
			} else {
				// No default branch, use cwd
				workDir = cwd
				slog.Debug("No default branch set, using cwd")
			}
		} else {
			// Not in a git repo, just use cwd
			workDir = cwd
			slog.Debug("Not in a git repo, using cwd")
		}
	}

	// Find all running VMs for this working directory
	slots, err := vm.FindVMsByWorkDir(workDir)
	if err != nil {
		return fmt.Errorf("failed to find VMs: %w", err)
	}

	if len(slots) == 0 {
		return fmt.Errorf("no VM found for working directory: %s", workDir)
	}

	var slot *vm.VMSlot
	if len(slots) == 1 {
		// Only one VM, use it directly
		slot = slots[0]
	} else {
		// Multiple VMs - show selection list
		fmt.Printf("Multiple VMs found for %s:\n\n", workDir)
		for i, s := range slots {
			fmt.Printf("  %d) slot %d - %s:%d (created %s)\n", i+1, s.SlotNumber, s.IPAddress, s.PortStart, formatRelativeTime(s.CreatedAt))
		}
		fmt.Printf("\nSelect VM [1-%d]: ", len(slots))

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > len(slots) {
			return fmt.Errorf("invalid selection: %s", input)
		}

		slot = slots[choice-1]
	}

	slog.Info("Connecting to VM", "ip", slot.IPAddress, "ssh_port", slot.PortStart, "working_dir", workDir)

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
