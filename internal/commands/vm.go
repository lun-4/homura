package commands

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
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

// VMHere, when true, makes vm commands target the VM for the current working
// directory instead of resolving a branch/worktree. It is set by the --here
// persistent flag on the vm command.
var VMHere *bool

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

	// HOMURA_DUMP_DOCKERFILE=1: dump the vm.lua-generated Dockerfile in this
	// process (the daemon may not have the env var).
	vm.DumpCustomDockerfile()

	// Resolve the branch (or default branch) to an existing worktree
	copyPath, branchName, err := resolveVMWorkDir(branchName, true)
	if err != nil {
		return err
	}

	slog.Info("Running VM in copy", "branch", branchName, "path", copyPath)

	// Create example vm.lua if config directory doesn't exist
	configDir := filepath.Join(os.Getenv("HOME"), ".config", "homura")
	examplePath := filepath.Join(configDir, "vm.lua.example")

	if _, err := os.Stat(examplePath); os.IsNotExist(err) {
		os.MkdirAll(configDir, 0755)

		exampleContent := `-- homura VM customization (vm.lua)
-- Copy to vm.lua and edit to apply. homura owns the FROM/base image, so you
-- never need to match a version. vm.json and Dockerfile.custom are ignored.

homura.config({
  -- cacheDir = "/var/cache/homura",
  -- stateDir = "/mnt/persistent/homura-vms",
  -- allowPaths = { "/home/you/projects", "/home/you/bin:ro" },
  -- snapshot = { "/home/you/projects" },
  -- maxSnapshots = 7,
})

homura.image(function(m)
  -- m:aptUpdate()
  -- m:aptInstall("vim", "tmux")             -- sorted, one RUN per package
  -- m:run("echo 'export PATH=$PATH:/x' >> /root/.profile")
  -- m:env("MY_VAR", "value")
  -- m:profile("export EDITOR=hx")
  -- m:fishProfile("set -gx EDITOR hx")
  -- m:curl("https://example.com/tool", "/tmp/tool")
  -- m:tarExtract("/tmp/tool.tar.xz", "/opt")
  -- m:symlinkFromHost("/home/you/.tool", "/root/.tool", {rw=true})
  -- m:linkDotClaudeFromHost()

  -- Core recipes:
  -- m:addDockerUbuntuRepo()
  -- m:installHelix("25.07.1")
  -- m:installGo("1.24.0")
  -- m:installElixir()
  -- m:installClaude("2.1.233")
  -- m:installRust()
  -- m:installPolytoken()
  -- m:installPi()
end)
`

		if err := os.WriteFile(examplePath, []byte(exampleContent), 0644); err != nil {
			slog.Warn("Failed to create example vm.lua", "error", err)
		} else {
			slog.Info("Created example vm.lua", "path", examplePath)
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

// sshReadyTimeout is how long `homura vm` waits for the guest's sshd before
// giving up and leaving the user to connect manually.
const sshReadyTimeout = 5 * time.Second

// waitForSSH polls until sshd behind ip:port presents its banner, or the
// timeout expires. A plain TCP connect is not enough: passt accepts the
// host-side connection itself before the guest port is open, so we only
// count a connection that actually greets us with "SSH-".
func waitForSSH(ip string, port int, timeout time.Duration) bool {
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.SetReadDeadline(time.Now().Add(time.Second))
			banner := make([]byte, 4)
			n, _ := conn.Read(banner)
			conn.Close()
			if n >= 4 && string(banner[:4]) == "SSH-" {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// execSSH replaces the current process with an interactive ssh session into
// the VM. The VM itself keeps running after the session ends (daemon mode).
func execSSH(ip string, port int) error {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh command not found: %w", err)
	}

	sshArgs := []string{"ssh", "-p", fmt.Sprintf("%d", port), fmt.Sprintf("root@%s", ip)}
	if err := syscall.Exec(sshPath, sshArgs, os.Environ()); err != nil {
		return fmt.Errorf("failed to exec ssh: %w", err)
	}

	// Unreachable if exec succeeds
	return nil
}

// runVMDetached starts the VM under the central daemon, streaming build/boot
// progress to stderr, then drops the user into an SSH session while the VM
// keeps running.
func runVMDetached(shareMode vm.ShareMode, copyPath string) error {
	// Don't start a second VM for a worktree that already has one; drop the
	// user into the running one instead.
	if existing, err := vm.FindVMsByWorkDir(copyPath); err == nil && len(existing) > 0 {
		s := existing[0]
		fmt.Printf("A VM is already running for this worktree (slot %d), connecting to it.\n", s.SlotNumber)
		fmt.Printf("  ssh:    ssh -p %d root@%s\n", s.PortStart, s.IPAddress)
		fmt.Printf("  attach: homura vm attach\n")
		fmt.Printf("  stop:   homura vm stop\n")
		return execSSH(s.IPAddress, s.PortStart)
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

	// Drop straight into the VM. Exiting the shell leaves the VM running;
	// stop it with `homura vm stop`.
	fmt.Println()
	fmt.Printf("[homura vm] Waiting for SSH (up to %s)...\n", sshReadyTimeout)
	sshWaitStart := time.Now()
	if !waitForSSH(result.IP, result.SSHPort, sshReadyTimeout) {
		fmt.Printf("[homura vm] SSH not ready after %s; the VM keeps booting in the background.\n", sshReadyTimeout)
		fmt.Println("[homura vm] Connect manually with: homura vm ssh")
		return nil
	}
	fmt.Printf("[homura vm] SSH ready after %s\n", time.Since(sshWaitStart).Round(time.Millisecond))
	return execSSH(result.IP, result.SSHPort)
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

	// --here targets the current working directory and skips all branch /
	// default-branch / worktree resolution. A branch arg is contradictory.
	if VMHere != nil && *VMHere {
		if branchName != "" {
			return "", "", fmt.Errorf("--here cannot be combined with a branch argument")
		}
		return cwd, "", nil
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

	// SSH is mapped to the first port in the slot's range
	return execSSH(slot.IPAddress, slot.PortStart)
}
