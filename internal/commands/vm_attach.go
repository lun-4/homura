package commands

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/term"
)

// ctrlRBracket is Ctrl-], the detach key.
const ctrlRBracket = 0x1d

// attachTailBytes is how much of the console log we replay as scrollback.
const attachTailBytes = 4096

// VMAttach connects an interactive serial console to a daemon-owned VM. QEMU's
// console chardev serves a single client, so we guard with an advisory lock and
// stream the raw socket directly (the daemon is not in the data path). Detach
// with Ctrl-]; the VM keeps running.
func VMAttach(branchName string) error {
	workDir, _, err := resolveVMWorkDir(branchName, true)
	if err != nil {
		return err
	}

	slot, err := selectVMSlot(workDir)
	if err != nil {
		return err
	}

	if slot.ConsoleSocket == "" {
		return fmt.Errorf("VM slot %d was started with --fg; its console is on that terminal, not attachable here", slot.SlotNumber)
	}

	// Advisory lock: QEMU's server chardev accepts only one client (a second
	// connect hangs silently), so fail fast if someone is already attached.
	lockPath := filepath.Join(filepath.Dir(slot.ConsoleSocket), "attach.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open attach lock: %w", err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("console for slot %d is already attached in another terminal", slot.SlotNumber)
		}
		return fmt.Errorf("failed to lock console: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// Replay recent console output for scrollback (cooked mode, before raw).
	if err := replayConsoleTail(slot.ConsoleLog); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not replay console log: %v\n", err)
	}
	fmt.Printf("[homura] attached to slot %d console - Ctrl-] to detach\n", slot.SlotNumber)

	conn, err := net.Dial("unix", slot.ConsoleSocket)
	if err != nil {
		return fmt.Errorf("failed to connect to console socket: %w", err)
	}
	defer conn.Close()

	// Put the terminal in raw mode so keystrokes reach the getty verbatim.
	// Skip it when stdin isn't a terminal (keeps attach scriptable).
	var oldState *term.State
	stdinFd := int(os.Stdin.Fd())
	if term.IsTerminal(stdinFd) {
		oldState, err = term.MakeRaw(stdinFd)
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %w", err)
		}
	}
	restored := false
	restore := func() {
		if oldState != nil && !restored {
			term.Restore(stdinFd, oldState)
			restored = true
		}
	}
	defer restore()

	// resultDisconnect: the console socket closed (VM stopped/died).
	// resultDetach: the user pressed Ctrl-].
	const (
		resultDisconnect = iota
		resultDetach
	)
	resultCh := make(chan int, 2)

	// conn -> stdout, verbatim.
	go func() {
		io.Copy(os.Stdout, conn)
		resultCh <- resultDisconnect
	}()

	// stdin -> conn, watching for the detach key.
	go func() {
		buf := make([]byte, 1024)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				if idx := bytes.IndexByte(buf[:n], ctrlRBracket); idx >= 0 {
					if idx > 0 {
						conn.Write(buf[:idx])
					}
					resultCh <- resultDetach
					return
				}
				if _, werr := conn.Write(buf[:n]); werr != nil {
					resultCh <- resultDisconnect
					return
				}
			}
			if rerr != nil {
				resultCh <- resultDisconnect
				return
			}
		}
	}()

	reason := <-resultCh

	// Restore the terminal before printing so newlines render correctly.
	restore()

	switch reason {
	case resultDetach:
		conn.Close() // unblock the conn->stdout copier
		fmt.Println("\n[homura] detached (VM still running)")
	default: // resultDisconnect
		fmt.Printf("\n[homura] console disconnected (VM stopped or exited)\n")
		fmt.Printf("[homura] console log: %s\n", slot.ConsoleLog)
	}
	return nil
}

// replayConsoleTail writes the last attachTailBytes of the console log to
// stdout, trimmed to a line boundary so scrollback doesn't start mid-line.
func replayConsoleTail(logPath string) error {
	if logPath == "" {
		return nil
	}
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	start := int64(0)
	if info.Size() > attachTailBytes {
		start = info.Size() - attachTailBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	// If we truncated mid-line, drop the partial leading line.
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	os.Stdout.Write(data)
	return nil
}
