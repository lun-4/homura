package vm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// VirtiofsManager manages a virtiofsd process for VM filesystem sharing
type VirtiofsManager struct {
	BinaryPath  string   // Path to virtiofsd binary (~/.cache/homura/bin/virtiofsd)
	SocketPath  string   // vhost-user socket for QEMU
	AdminPort   int      // HTTP admin API port
	VMPort      int      // VM request API port
	AdminToken  string   // Bearer token for admin API
	VMToken     string   // Bearer token for VM API
	Cmd         *exec.Cmd
	HomeDir     string   // Host user's home directory
	WorkDir     string   // Working directory to expose
	ExtraPaths  []PathSpec // Additional paths to expose
	SlotNumber  int      // VM slot number for identification
}

// NewVirtiofsManager creates a new VirtiofsManager instance
func NewVirtiofsManager(stateDir, homeDir, workDir string, slotNumber int, extraPaths []PathSpec) (*VirtiofsManager, error) {
	// Get virtiofsd binary from cache directory
	root, err := CacheDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get cache directory: %w", err)
	}
	binaryPath := filepath.Join(root, "bin", "virtiofsd")
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("virtiofsd binary not found at %s (run 'make virtiofs' to build)", binaryPath)
	}

	socketPath := filepath.Join(stateDir, "virtiofs.sock")

	// Generate tokens
	adminToken, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate admin token: %w", err)
	}
	vmToken, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate VM token: %w", err)
	}

	// Allocate random ports for admin and VM APIs
	adminPort, err := findAvailablePort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate admin port: %w", err)
	}
	vmPort, err := findAvailablePort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate VM port: %w", err)
	}

	return &VirtiofsManager{
		BinaryPath:  binaryPath,
		SocketPath:  socketPath,
		AdminPort:   adminPort,
		VMPort:      vmPort,
		AdminToken:  adminToken,
		VMToken:     vmToken,
		HomeDir:     homeDir,
		WorkDir:     workDir,
		ExtraPaths:  extraPaths,
		SlotNumber:  slotNumber,
	}, nil
}

// findAvailablePort finds an available TCP port by binding to port 0
func findAvailablePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port, nil
}

// generateToken creates a random 256-bit token
func generateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// Start launches the virtiofsd process
func (v *VirtiofsManager) Start() error {
	slog.Info("Starting virtiofsd", "socket", v.SocketPath, "admin_port", v.AdminPort, "vm_port", v.VMPort)

	// Build arguments
	args := []string{
		"--socket-path=" + v.SocketPath,
		"--shared-dir=/",
		"--sandbox=none",
		"--seccomp=none",
		"--http-control",
		fmt.Sprintf("--admin-api-port=%d", v.AdminPort),
		fmt.Sprintf("--vm-api-port=%d", v.VMPort),
		fmt.Sprintf("--admin-token=%s", v.AdminToken),
		fmt.Sprintf("--vm-token=%s", v.VMToken),
		fmt.Sprintf("--vm-name=%s:%d", v.WorkDir, v.SlotNumber),
	}

	// Map guest root <-> our own uid/gid so shared files appear root-owned
	// in the VM (the guest only has root) and guest-created files land
	// owned by us on the host.
	args = append(args,
		fmt.Sprintf("--translate-uid=map:0:%d:1", os.Getuid()),
		fmt.Sprintf("--translate-gid=map:0:%d:1", os.Getgid()),
	)

	// Add working directory (always read-write)
	args = append(args, fmt.Sprintf("--share=%s:rw", v.WorkDir))

	// Add Claude config paths if they exist
	claudeJson := filepath.Join(v.HomeDir, ".claude.json")
	claudeDir := filepath.Join(v.HomeDir, ".claude")
	if _, err := os.Stat(claudeJson); err == nil {
		args = append(args, fmt.Sprintf("--share=%s:rw", claudeJson))
		slog.Info("Auto-exposing Claude config", "path", claudeJson)
	}
	if _, err := os.Stat(claudeDir); err == nil {
		args = append(args, fmt.Sprintf("--share=%s:rw", claudeDir))
		slog.Info("Auto-exposing Claude config", "path", claudeDir)
	}
	claudeLock := filepath.Join(v.HomeDir, ".claude.lock")
	args = append(args, fmt.Sprintf("--share=%s:rw", claudeLock))
	slog.Info("Auto-exposing Claude lock", "path", claudeLock)

	// Add VM CLAUDE.md customizations (read-only)
	vmClaudeMd := filepath.Join(v.HomeDir, ".config", "homura", "CLAUDE.md")
	if _, err := os.Stat(vmClaudeMd); err == nil {
		args = append(args, fmt.Sprintf("--share=%s:ro", vmClaudeMd))
		slog.Info("Auto-exposing VM CLAUDE.md (read-only)", "path", vmClaudeMd)
	}

	// Add extra configured paths
	for _, spec := range v.ExtraPaths {
		mode := "rw"
		if spec.ReadOnly {
			mode = "ro"
		}
		args = append(args, fmt.Sprintf("--share=%s:%s", spec.Path, mode))
		slog.Info("Adding configured path", "path", spec.Path, "readonly", spec.ReadOnly)
	}

	v.Cmd = exec.Command(v.BinaryPath, args...)
	v.Cmd.Stdout = ChildOutput
	v.Cmd.Stderr = ChildOutput
	if ChildProcAttr != nil {
		v.Cmd.SysProcAttr = ChildProcAttr
	}

	if err := v.Cmd.Start(); err != nil {
		return fmt.Errorf("failed to start virtiofsd: %w", err)
	}

	slog.Info("virtiofsd started", "pid", v.Cmd.Process.Pid)

	// Wait for socket to be ready
	if err := v.WaitReady(5 * time.Second); err != nil {
		v.Stop()
		return fmt.Errorf("virtiofsd failed to become ready: %w", err)
	}

	// Write token file for host-side control
	if _, err := v.WriteTokenFile(); err != nil {
		v.Stop()
		return fmt.Errorf("failed to write token file: %w", err)
	}

	return nil
}

// WaitReady waits for the virtiofsd socket to be created
func (v *VirtiofsManager) WaitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(v.SocketPath); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for virtiofsd socket at %s", v.SocketPath)
}

// Stop terminates the virtiofsd process and cleans up
func (v *VirtiofsManager) Stop() error {
	if v.Cmd != nil && v.Cmd.Process != nil {
		slog.Info("Stopping virtiofsd", "pid", v.Cmd.Process.Pid)
		if err := v.Cmd.Process.Kill(); err != nil {
			slog.Warn("Failed to kill virtiofsd", "error", err)
		}
		v.Cmd.Wait() // Reap zombie
	}

	// Remove socket file
	if v.SocketPath != "" {
		os.Remove(v.SocketPath)
	}

	return nil
}

// GetPID returns the virtiofsd process ID
func (v *VirtiofsManager) GetPID() int {
	if v.Cmd != nil && v.Cmd.Process != nil {
		return v.Cmd.Process.Pid
	}
	return 0
}

// WriteTokenFile writes the virtiofsd token file for host-side control
func (v *VirtiofsManager) WriteTokenFile() (string, error) {
	if v.Cmd == nil || v.Cmd.Process == nil {
		return "", fmt.Errorf("virtiofsd not running")
	}

	tokenFile := fmt.Sprintf("/tmp/virtiofs-token-%d", v.Cmd.Process.Pid)
	content := fmt.Sprintf("%s\n%d\n%d\n", v.AdminToken, v.AdminPort, v.VMPort)

	if err := os.WriteFile(tokenFile, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("failed to write token file: %w", err)
	}

	return tokenFile, nil
}

// RemoveTokenFile removes the virtiofsd token file
func (v *VirtiofsManager) RemoveTokenFile() {
	if v.Cmd != nil && v.Cmd.Process != nil {
		tokenFile := fmt.Sprintf("/tmp/virtiofs-token-%d", v.Cmd.Process.Pid)
		os.Remove(tokenFile)
	}
}
