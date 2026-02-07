package vm

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

// PasstManager manages the passt networking daemon for a VM
type PasstManager struct {
	SocketPath string
	IPAddress  string
	PortStart  int
	PortEnd    int
	Cmd        *exec.Cmd
}

// IsPasstAvailable checks if the passt binary is available in PATH
func IsPasstAvailable() bool {
	_, err := exec.LookPath("passt")
	return err == nil
}

// NewPasstManager creates a new passt manager for the given VM slot
func NewPasstManager(stateDir string, slot *VMSlot) (*PasstManager, error) {
	// Socket path is in the VM's state directory
	socketPath := filepath.Join(stateDir, "passt.sock")

	return &PasstManager{
		SocketPath: socketPath,
		IPAddress:  slot.IPAddress,
		PortStart:  slot.PortStart,
		PortEnd:    slot.PortEnd,
	}, nil
}

// Start launches the passt daemon in the background
func (pm *PasstManager) Start() error {
	if !IsPasstAvailable() {
		return fmt.Errorf("passt is required but not found in PATH\n\n" +
			"homura vm requires passt for networking. Please install it:\n" +
			"  • Debian/Ubuntu: apt install passt\n" +
			"  • Arch Linux: pacman -S passt\n" +
			"  • Fedora: dnf install passt\n\n" +
			"For more info: https://passt.top/")
	}

	// Remove stale socket if it exists
	if err := os.Remove(pm.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove stale socket: %w", err)
	}

	// Build passt command
	// Map first port to SSH (port 22 in VM), rest are 1:1
	// Example: external 10000 → internal 22 (SSH)
	//          external 10001-10009 → internal 10001-10009
	sshPortMap := fmt.Sprintf("%s/%d:22", pm.IPAddress, pm.PortStart)
	portRange := fmt.Sprintf("%d-%d", pm.PortStart+1, pm.PortEnd)
	tcpRangeForward := fmt.Sprintf("%s/%s", pm.IPAddress, portRange)
	udpRangeForward := fmt.Sprintf("%s/%s", pm.IPAddress, portRange)

	args := []string{
		"-f",                // Run in foreground (we manage it)
		"-s", pm.SocketPath, // Unix socket path for QEMU
		"-t", sshPortMap, // TCP SSH port mapping (external → 22)
		"-t", tcpRangeForward, // TCP port forwarding for rest (1:1)
		"-u", udpRangeForward, // UDP port forwarding (1:1)
		"-a", "10.0.2.15", // Guest IP address (static for all VMs)
		"-n", "24", // Network prefix length
		"-g", "10.0.2.2", // Gateway IP
	}

	pm.Cmd = exec.Command("passt", args...)

	// Log passt output to slog
	pm.Cmd.Stdout = os.Stderr
	pm.Cmd.Stderr = os.Stderr

	slog.Info("Starting passt",
		"socket", pm.SocketPath,
		"ip", pm.IPAddress,
		"port_range", portRange)

	if err := pm.Cmd.Start(); err != nil {
		return fmt.Errorf("failed to start passt: %w", err)
	}

	// Wait a moment for the socket to be created
	// TODO: Could improve this with a proper wait loop checking for socket existence
	// For now, passt creates the socket very quickly so this should be fine

	return nil
}

// Stop terminates the passt daemon and cleans up the socket
func (pm *PasstManager) Stop() error {
	if pm.Cmd == nil || pm.Cmd.Process == nil {
		return nil
	}

	slog.Info("Stopping passt", "pid", pm.Cmd.Process.Pid)

	// Kill the passt process
	if err := pm.Cmd.Process.Kill(); err != nil {
		slog.Warn("Failed to kill passt process", "error", err)
	}

	// Wait for it to exit
	_ = pm.Cmd.Wait()

	// Clean up the socket
	if err := os.Remove(pm.SocketPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove passt socket", "error", err)
	}

	return nil
}
