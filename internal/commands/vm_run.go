package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/lun-4/homura/internal/daemon"
	"github.com/lun-4/homura/internal/vm"
)

// shellQuote single-quotes s for the remote POSIX shell, escaping embedded
// single quotes as '\''.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// splitRunArgs recovers the branch (if any) and the command to run from the
// positional args cobra passes to `vm run`. cobra strips the literal `--`
// delimiter, so we rescan os.Args to find it: the first `--` always separates
// the optional branch from the command (a command's own `--` come after).
func splitRunArgs(args []string) (branch string, command []string) {
	sep := -1
	for i, a := range os.Args[1:] {
		if a == "--" {
			sep = i
			break
		}
	}

	if sep == -1 {
		return "", args
	}

	command = os.Args[sep+2:]
	// cobra arranges args as: [optional branch] + command. If args has more
	// elements than the command, the excess prefix is the branch.
	if len(args) > len(command) {
		branch = args[0]
	}
	return branch, command
}

// buildRemoteCmd returns a shell snippet that cds into the repo's worktree
// copy inside the guest (under /mnt/host) and runs command as a single
// invocation: command[0] is the executable and the rest are its args.
func buildRemoteCmd(workDir string, command []string) string {
	cmd := "cd " + shellQuote("/mnt/host"+workDir)
	if len(command) == 0 {
		return cmd
	}
	quoted := make([]string, len(command))
	for i, c := range command {
		quoted[i] = shellQuote(c)
	}
	return cmd + " && " + strings.Join(quoted, " ")
}

// execSSHCommand replaces the current process with ssh running remoteCmd in
// the VM. It requests a pty (-t) only when stdin is a char device so
// interactive TUIs get a terminal while piped/scripted use still works.
func execSSHCommand(ip string, port int, remoteCmd string) error {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh command not found: %w", err)
	}

	sshArgs := []string{"ssh", "-p", fmt.Sprintf("%d", port)}
	if stdinIsTTY() {
		sshArgs = append(sshArgs, "-t")
	}
	sshArgs = append(sshArgs, fmt.Sprintf("root@%s", ip), remoteCmd)

	if err := syscall.Exec(sshPath, sshArgs, os.Environ()); err != nil {
		return fmt.Errorf("failed to exec ssh: %w", err)
	}
	return nil
}

// stdinIsTTY reports whether os.Stdin is attached to a terminal (char device).
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// VMRun implements the `homura vm run` command: it ensures a VM is running for
// the target worktree (reusing one if present, otherwise starting it under the
// daemon and waiting for SSH), then runs <command> inside the guest with cwd
// set to the repo's worktree copy at /mnt/host<copyPath>.
func VMRun(args []string, shareModeStr string) error {
	shareMode, err := vm.ParseShareMode(shareModeStr)
	if err != nil {
		return err
	}

	branch, command := splitRunArgs(args)
	if len(command) == 0 {
		return fmt.Errorf("missing command after --")
	}

	slot, err := resolveVMRunSlot(branch, shareMode)
	if err != nil {
		return err
	}

	return execSSHCommand(slot.IPAddress, slot.PortStart, buildRemoteCmd(slot.WorkingDir, command))
}

// resolveVMRunSlot returns the *vm.VMSlot to run the command in. A numeric
// branch is treated as a vmid and must already be running. Otherwise the
// branch/default is resolved to a worktree copy: an existing VM for it is
// reused, else a new one is started under the daemon and we wait for SSH.
func resolveVMRunSlot(branch string, shareMode vm.ShareMode) (*vm.VMSlot, error) {
	// --here rejects any branch/slot arg; cwd resolution happens below in
	// resolveVMWorkDir (which, with --here, returns the cwd).
	if VMHere != nil && *VMHere {
		if branch != "" {
			return nil, fmt.Errorf("--here cannot be combined with a branch/slot argument")
		}
	}

	if branch != "" {
		if slotNum, err := strconv.Atoi(branch); err == nil {
			slot, err := vm.FindVMBySlot(slotNum)
			if err != nil {
				return nil, fmt.Errorf("no running VM for slot %d", slotNum)
			}
			return slot, nil
		}
	}

	copyPath, _, err := resolveVMWorkDir(branch, true)
	if err != nil {
		return nil, err
	}

	if existing, err := vm.FindVMsByWorkDir(copyPath); err == nil && len(existing) > 0 {
		return existing[0], nil
	}

	client, err := daemon.EnsureDaemon()
	if err != nil {
		return nil, fmt.Errorf("failed to reach homura daemon: %w", err)
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
		return nil, fmt.Errorf("failed to start VM: %w", err)
	}

	if !waitForSSH(result.IP, result.SSHPort, sshReadyTimeout) {
		return nil, fmt.Errorf("ssh not ready after %s; the VM keeps booting in the background. Connect manually with: homura vm ssh", sshReadyTimeout)
	}

	return &vm.VMSlot{
		IPAddress:  result.IP,
		PortStart:  result.SSHPort,
		WorkingDir: copyPath,
	}, nil
}