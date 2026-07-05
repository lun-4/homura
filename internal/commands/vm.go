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
	"github.com/lun-4/homura/internal/daemon"
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

// RunVM implements the `homura vm` command. By default it starts the VM
// detached under the central daemon (streaming build/boot progress and then
// returning); with foreground=true (--fg) it keeps today's behavior of running
// QEMU chained to this terminal.
func RunVM(cmd *cobra.Command, args []string, branchName string, shareModeStr string, foreground bool) error {
	// Parse and validate share mode
	shareMode, err := vm.ParseShareMode(shareModeStr)
	if err != nil {
		return err
	}

	slog.Info("Starting homura VM", "share_mode", shareMode, "foreground", foreground)

	// Resolve the branch (or default branch) to an existing worktree
	copyPath, branchName, err := resolveVMWorkDir(branchName, true)
	if err != nil {
		return err
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

	if foreground {
		return runVMForeground(shareMode, copyPath)
	}
	return runVMDetached(shareMode, copyPath)
}

// runVMForeground runs QEMU chained to this terminal (today's behavior).
func runVMForeground(shareMode vm.ShareMode, copyPath string) error {
	// NewVM no longer changes the process directory itself; do it here so the
	// foreground VM behaves exactly as before for any cwd-relative work.
	if err := os.Chdir(copyPath); err != nil {
		return fmt.Errorf("failed to change to copy directory: %w", err)
	}

	vmInstance, err := vm.NewVM(shareMode, copyPath, "")
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
	if err := vmInstance.Start(vm.StartOptions{Foreground: true}); err != nil {
		return fmt.Errorf("failed to start VM: %w", err)
	}

	// Wait for VM to exit
	if err := vmInstance.Wait(); err != nil {
		slog.Debug("VM wait returned", "error", err)
	}

	slog.Info("VM shutdown complete")
	return nil
}

// runVMDetached starts the VM under the central daemon, streaming build/boot
// progress to stderr, then returns while the VM keeps running.
func runVMDetached(shareMode vm.ShareMode, copyPath string) error {
	// Don't start a second VM for a worktree that already has one; point the
	// user at the running one instead.
	if existing, err := vm.FindVMsByWorkDir(copyPath); err == nil && len(existing) > 0 {
		s := existing[0]
		fmt.Printf("A VM is already running for this worktree (slot %d).\n", s.SlotNumber)
		fmt.Printf("  ssh:    ssh -p %d root@%s\n", s.PortStart, s.IPAddress)
		fmt.Printf("  attach: homura vm attach\n")
		fmt.Printf("  stop:   homura vm stop\n")
		return nil
	}

	client, err := daemon.EnsureDaemon()
	if err != nil {
		return fmt.Errorf("failed to reach homura daemon: %w", err)
	}

	params := daemon.StartVMParams{
		WorkDir:      copyPath,
		ShareMode:    string(shareMode),
		StateDirBase: os.Getenv("HOMURA_VM_STATE_DIR"),
	}

	var result daemon.VMResult
	err = client.CallStream(daemon.MethodStartVM, params, func(line string) {
		fmt.Fprintln(os.Stderr, line)
	}, &result)
	if err != nil {
		return fmt.Errorf("failed to start VM: %w", err)
	}

	fmt.Println()
	fmt.Println("[homura vm] microvm started (running under homura daemon)")
	fmt.Printf("[homura vm] SSH: ssh -p %d root@%s\n", result.SSHPort, result.IP)
	fmt.Printf("[homura vm] Slot: %d (IP: %s, Ports: %d-%d)\n",
		result.Slot, result.IP, result.PortStart, result.PortEnd)
	fmt.Printf("[homura vm] Console log: %s\n", result.ConsoleLog)
	fmt.Println("[homura vm] Attach to console: homura vm attach")
	fmt.Println("[homura vm] Stop VM:           homura vm stop")
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
		if s.ConsoleLog != "" {
			fmt.Printf("       console %s\n", s.ConsoleLog)
		}
	}

	fmt.Printf("\n%d VM(s) running\n", len(slots))
	return nil
}

// resolveVMWorkDir resolves a branch name (or, if empty, the default branch,
// or the current directory) to the working directory a VM command should
// target. It returns the resolved directory and the branch name it came
// from (empty if the directory came from cwd rather than a worktree).
//
// When requireCopy is true, a missing default branch or missing worktree is
// an error naming the branch to clone (RunVM's behavior: it always needs a
// worktree to boot a VM in). When false, that same ambiguity falls back to
// the current working directory instead of failing (VMSsh's behavior: best
// effort at finding "the" VM for wherever you are).
func resolveVMWorkDir(branchName string, requireCopy bool) (workDir string, resolvedBranch string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("failed to get current directory: %w", err)
	}

	if branchName != "" {
		repoRoot, err := git.GetRepoRoot(cwd)
		if err != nil {
			return "", "", fmt.Errorf("not a git repository: %w", err)
		}
		copyPath := git.GetCopyPath(repoRoot, branchName)
		if _, err := os.Stat(copyPath); os.IsNotExist(err) {
			return "", "", fmt.Errorf("copy does not exist at %s\nRun 'homura clone %s' first", copyPath, branchName)
		}
		return copyPath, branchName, nil
	}

	if requireCopy {
		repoRoot, err := git.GetRepoRoot(cwd)
		if err != nil {
			return "", "", fmt.Errorf("not a git repository: %w", err)
		}
		state, err := config.LoadState(repoRoot)
		if err != nil {
			return "", "", fmt.Errorf("failed to load state: %w", err)
		}
		if state.DefaultBranch == "" {
			return "", "", fmt.Errorf("no default branch set and no branch name provided")
		}
		copyPath := git.GetCopyPath(repoRoot, state.DefaultBranch)
		if _, err := os.Stat(copyPath); os.IsNotExist(err) {
			return "", "", fmt.Errorf("copy does not exist at %s\nRun 'homura clone %s' first", copyPath, state.DefaultBranch)
		}
		return copyPath, state.DefaultBranch, nil
	}

	// No branch given, and a missing/ambiguous worktree isn't fatal here -
	// fall back to cwd.
	repoRoot, err := git.GetRepoRoot(cwd)
	if err != nil {
		slog.Debug("Not in a git repo, using cwd")
		return cwd, "", nil
	}
	state, err := config.LoadState(repoRoot)
	if err != nil || state.DefaultBranch == "" {
		slog.Debug("No default branch set, using cwd")
		return cwd, "", nil
	}
	copyPath := git.GetCopyPath(repoRoot, state.DefaultBranch)
	if _, err := os.Stat(copyPath); err != nil {
		slog.Debug("Default branch copy doesn't exist, using cwd")
		return cwd, "", nil
	}
	slog.Debug("Using default branch", "branch", state.DefaultBranch)
	return copyPath, state.DefaultBranch, nil
}

// selectVMSlot finds the running VM(s) for workDir. If there's exactly one,
// it's returned directly; if there are several, the user is prompted to pick
// one interactively.
func selectVMSlot(workDir string) (*vm.VMSlot, error) {
	slots, err := vm.FindVMsByWorkDir(workDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find VMs: %w", err)
	}

	if len(slots) == 0 {
		return nil, fmt.Errorf("no VM found for working directory: %s", workDir)
	}

	if len(slots) == 1 {
		return slots[0], nil
	}

	// Multiple VMs - show selection list
	fmt.Printf("Multiple VMs found for %s:\n\n", workDir)
	for i, s := range slots {
		fmt.Printf("  %d) slot %d - %s:%d (created %s)\n", i+1, s.SlotNumber, s.IPAddress, s.PortStart, formatRelativeTime(s.CreatedAt))
	}
	fmt.Printf("\nSelect VM [1-%d]: ", len(slots))

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(slots) {
		return nil, fmt.Errorf("invalid selection: %s", input)
	}

	return slots[choice-1], nil
}

// VMSsh implements the `homura vm ssh` command
func VMSsh(cmd *cobra.Command, args []string, branchName string) error {
	workDir, _, err := resolveVMWorkDir(branchName, false)
	if err != nil {
		return err
	}

	slot, err := selectVMSlot(workDir)
	if err != nil {
		return err
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
