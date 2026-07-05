package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RuntimeDir returns the per-user runtime directory homura uses for the
// daemon's control socket and lock file. It prefers $XDG_RUNTIME_DIR/homura
// (a tmpfs cleaned on logout) and falls back to /tmp/homura-<uid>. The
// directory is created 0700.
func RuntimeDir() (string, error) {
	var base string
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		base = filepath.Join(xdg, "homura")
	} else {
		base = fmt.Sprintf("/tmp/homura-%d", os.Getuid())
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", fmt.Errorf("failed to create runtime dir %s: %w", base, err)
	}
	return base, nil
}

// SocketPath returns the path to the daemon's control socket.
func SocketPath() (string, error) {
	dir, err := RuntimeDir()
	if err != nil {
		return "", err
	}
	sock := filepath.Join(dir, "daemon.sock")
	// Unix socket paths (sun_path) are limited to ~108 bytes; fail with a
	// clear message instead of bind/connect's "invalid argument".
	if len(sock) > 104 {
		return "", fmt.Errorf("daemon socket path too long for unix sockets (%d > 104 chars): %s\n"+
			"Set XDG_RUNTIME_DIR to a shorter path", len(sock), sock)
	}
	return sock, nil
}

// LockPath returns the path to the daemon's flock file.
func LockPath() (string, error) {
	dir, err := RuntimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.lock"), nil
}

// LogDir returns the directory for daemon and console logs. Unlike the
// per-VM state dir (removed on cleanup and by stale-slot GC), logs live here
// so they survive for postmortem. Created 0755.
func LogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	dir := filepath.Join(home, ".cache", "homura", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create log dir %s: %w", dir, err)
	}
	return dir, nil
}

// DaemonLogPath returns the path to the daemon's own log file.
func DaemonLogPath() (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}

// ConsoleLogPath returns a unique console log path for the given VM slot.
// The unix-ms suffix keeps successive VMs on the same slot from clobbering
// each other's logs.
func ConsoleLogPath(slot int) (string, error) {
	dir, err := LogDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("console-slot%d-%d.log", slot, time.Now().UnixMilli())), nil
}
