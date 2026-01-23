package vm

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

// VM represents a running homura VM instance
type VM struct {
	WorkDir         string      // Current working directory
	SSHPort         int         // Detected free port for SSH
	SSHPubPath      string      // Path to user's SSH public key
	QEMUCmd         *exec.Cmd   // QEMU process
	StateDir        string      // Temporary state directory
	Images          *ImagePaths
	EphemeralDisk   string      // Path to ephemeral rootfs disk
}

// NewVM creates a new VM instance with detected configuration
func NewVM() (*VM, error) {
	slog.Info("Initializing new VM instance")

	// Detect current working directory
	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	// Find free SSH port
	sshPort, err := FindFreePort()
	if err != nil {
		return nil, fmt.Errorf("failed to find free port: %w", err)
	}

	// Create temporary state directory
	stateDir, err := os.MkdirTemp("", "homura-vm-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	// Use user's SSH key instead of generating ephemeral one
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Try common SSH key locations
	sshPubPath := ""
	candidates := []string{
		filepath.Join(homeDir, ".ssh", "id_ed25519.pub"),
		filepath.Join(homeDir, ".ssh", "id_rsa.pub"),
		filepath.Join(homeDir, ".ssh", "id_ecdsa.pub"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			sshPubPath = path
			break
		}
	}
	if sshPubPath == "" {
		return nil, fmt.Errorf("no SSH public key found in ~/.ssh/ (tried: id_ed25519.pub, id_rsa.pub, id_ecdsa.pub)")
	}

	vm := &VM{
		WorkDir:    workDir,
		SSHPort:    sshPort,
		SSHPubPath: sshPubPath,
		StateDir:   stateDir,
	}

	slog.Info("VM instance initialized",
		"workdir", workDir,
		"ssh_port", sshPort,
		"ssh_key", sshPubPath)

	return vm, nil
}

// Start starts the VM
func (vm *VM) Start() error {
	slog.Info("Starting VM")

	// Ensure images are downloaded and built
	images, err := EnsureImages(vm.SSHPubPath)
	if err != nil {
		return fmt.Errorf("failed to ensure images: %w", err)
	}
	vm.Images = images

	// Create ephemeral disk
	ephemeralDisk := filepath.Join(vm.StateDir, "rootfs-ephemeral.ext4")
	if err := CreateEphemeralDisk(images.RootfsPath, ephemeralDisk, "10G"); err != nil {
		return fmt.Errorf("failed to create ephemeral disk: %w", err)
	}
	vm.EphemeralDisk = ephemeralDisk

	// Build QEMU configuration
	cfg := &QEMUConfig{
		KernelPath:    images.KernelPath,
		InitrdPath:    images.InitramfsPath,
		RootfsPath:    ephemeralDisk,
		Memory:        2048, // 2GB
		CPUs:          4,
		SSHPort:       vm.SSHPort,
	}

	// Build QEMU command
	args := BuildQEMUArgs(cfg)

	// Create QEMU command
	vm.QEMUCmd = exec.Command("qemu-system-x86_64", args...)
	vm.QEMUCmd.Stdin = os.Stdin
	vm.QEMUCmd.Stdout = os.Stdout
	vm.QEMUCmd.Stderr = os.Stderr

	// Start QEMU
	slog.Info("Launching QEMU")
	if err := vm.QEMUCmd.Start(); err != nil {
		return fmt.Errorf("failed to start QEMU: %w", err)
	}

	slog.Info("QEMU process started", "pid", vm.QEMUCmd.Process.Pid)

	// Display connection info
	vm.DisplayConnectionInfo()

	return nil
}

// Wait waits for the VM to exit
func (vm *VM) Wait() error {
	if vm.QEMUCmd == nil || vm.QEMUCmd.Process == nil {
		return fmt.Errorf("VM not running")
	}

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Wait for QEMU to exit or signal
	done := make(chan error, 1)
	go func() {
		done <- vm.QEMUCmd.Wait()
	}()

	select {
	case <-sigCh:
		slog.Info("Received interrupt signal, shutting down VM...")
		if vm.QEMUCmd.Process != nil {
			vm.QEMUCmd.Process.Signal(syscall.SIGTERM)
		}
		<-done // Wait for process to exit
		return nil
	case err := <-done:
		if err != nil {
			return fmt.Errorf("QEMU exited with error: %w", err)
		}
		slog.Info("VM exited normally")
		return nil
	}
}

// Cleanup removes temporary state files
func (vm *VM) Cleanup() error {
	slog.Info("Cleaning up VM state", "state_dir", vm.StateDir)

	// Remove ephemeral disk
	if vm.EphemeralDisk != "" {
		if err := os.Remove(vm.EphemeralDisk); err != nil && !os.IsNotExist(err) {
			slog.Warn("Failed to remove ephemeral disk", "error", err)
		}
	}

	// Remove state directory
	if err := os.RemoveAll(vm.StateDir); err != nil {
		return fmt.Errorf("failed to remove state directory: %w", err)
	}

	return nil
}

// DisplayConnectionInfo prints connection information to the user
func (vm *VM) DisplayConnectionInfo() {
	fmt.Println()
	fmt.Println("[homura vm] Starting microvm...")
	fmt.Printf("[homura vm] SSH: ssh -p %d root@localhost\n", vm.SSHPort)
	fmt.Println("[homura vm] Press Ctrl+C to stop")
	fmt.Println()
}
