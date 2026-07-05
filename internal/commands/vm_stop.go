package commands

import (
	"errors"
	"fmt"
	"log/slog"
	"syscall"
	"time"

	"github.com/lun-4/homura/internal/daemon"
	"github.com/lun-4/homura/internal/vm"
)

// VMStop stops a running VM. Daemon-owned VMs (those with a console socket) are
// stopped via the daemon's stop-vm RPC; if the daemon is gone, or for
// foreground-owned VMs, it signals the owning process directly and waits for
// the slot to be reclaimed.
func VMStop(branchName string) error {
	workDir, _, err := resolveVMWorkDir(branchName, true)
	if err != nil {
		return err
	}

	slot, err := selectVMSlot(workDir)
	if err != nil {
		return err
	}

	// pid is the process we fall back to signalling if we don't (or can't) use
	// the daemon RPC path.
	var pid int

	if slot.ConsoleSocket != "" {
		// Daemon-owned. Try the RPC first, but never spawn a daemon just to
		// stop a VM - only Dial an existing one.
		if client, derr := daemon.Dial(); derr == nil {
			var res daemon.StopVMResult
			if err := client.Call(daemon.MethodStopVM,
				daemon.StopVMParams{Slot: slot.SlotNumber}, &res); err != nil {
				return fmt.Errorf("failed to stop VM via daemon: %w", err)
			}
			fmt.Printf("Stopped VM slot %d.\n", res.Slot)
			if res.ConsoleLog != "" {
				fmt.Printf("Console log preserved at: %s\n", res.ConsoleLog)
			}
			return nil
		}
		// Daemon unreachable but the slot row still exists - the daemon likely
		// died and QEMU may be orphaned. Signal QEMU directly.
		slog.Warn("daemon not reachable, signalling QEMU directly", "slot", slot.SlotNumber)
		pid = slot.QEMUPID
		if pid <= 0 {
			pid = slot.VMPID
		}
	} else {
		// Foreground-owned: signal the fg homura process, which runs its own
		// Wait() signal handler + cleanup.
		pid = slot.VMPID
	}

	slog.Info("Stopping VM via SIGTERM", "slot", slot.SlotNumber, "pid", pid)
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("failed to signal VM process %d: %w", pid, err)
	}

	// Poll until the slot row disappears. FindVMBySlot triggers stale-slot
	// cleanup, so a dead PID's row is reaped here even without the daemon.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := vm.FindVMBySlot(slot.SlotNumber); err != nil {
			fmt.Printf("Stopped VM slot %d.\n", slot.SlotNumber)
			if slot.ConsoleLog != "" {
				fmt.Printf("Console log preserved at: %s\n", slot.ConsoleLog)
			}
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("VM slot %d did not stop within 15s (pid %d may still be alive)", slot.SlotNumber, pid)
}
