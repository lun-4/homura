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
	WorkDir       string        // Current working directory
	SSHPubPath    string        // Path to user's SSH public key
	QEMUCmd       *exec.Cmd     // QEMU process
	StateDir      string        // Temporary state directory
	Images        *ImagePaths
	EphemeralDisk string        // Path to ephemeral rootfs disk

	// Passt networking fields
	SlotNumber   int            // Sequential slot (1-254)
	IPAddress    string         // 127.0.0.X
	PortStart    int            // First port in range (e.g., 10000)
	PortEnd      int            // Last port in range (e.g., 10009)
	PasstManager *PasstManager  // Passt daemon manager
}

// NewVM creates a new VM instance with detected configuration
func NewVM() (*VM, error) {
	slog.Info("Initializing new VM instance")

	// Check if passt is available
	if !IsPasstAvailable() {
		return nil, fmt.Errorf("passt is required but not found in PATH\n\n" +
			"homura vm requires passt for networking. Please install it:\n" +
			"  • Debian/Ubuntu: apt install passt\n" +
			"  • Arch Linux: pacman -S passt\n" +
			"  • Fedora: dnf install passt\n\n" +
			"For more info: https://passt.top/")
	}

	// Detect current working directory
	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	// Create temporary state directory
	stateDir, err := os.MkdirTemp("", "homura-vm-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	// Allocate VM slot (IP and port range)
	socketPath := filepath.Join(stateDir, "passt.sock")
	slot, err := AllocateVMSlot(socketPath)
	if err != nil {
		os.RemoveAll(stateDir) // Clean up state dir on error
		return nil, fmt.Errorf("failed to allocate VM slot: %w", err)
	}

	// Create passt manager
	passtMgr, err := NewPasstManager(stateDir, slot)
	if err != nil {
		ReleaseVMSlot(slot.SlotNumber) // Release slot on error
		os.RemoveAll(stateDir)
		return nil, fmt.Errorf("failed to create passt manager: %w", err)
	}

	// Use user's SSH key instead of generating ephemeral one
	homeDir, err := os.UserHomeDir()
	if err != nil {
		ReleaseVMSlot(slot.SlotNumber)
		os.RemoveAll(stateDir)
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
		ReleaseVMSlot(slot.SlotNumber)
		os.RemoveAll(stateDir)
		return nil, fmt.Errorf("no SSH public key found in ~/.ssh/ (tried: id_ed25519.pub, id_rsa.pub, id_ecdsa.pub)")
	}

	vm := &VM{
		WorkDir:      workDir,
		SSHPubPath:   sshPubPath,
		StateDir:     stateDir,
		SlotNumber:   slot.SlotNumber,
		IPAddress:    slot.IPAddress,
		PortStart:    slot.PortStart,
		PortEnd:      slot.PortEnd,
		PasstManager: passtMgr,
	}

	slog.Info("VM instance initialized",
		"workdir", workDir,
		"slot", slot.SlotNumber,
		"ip", slot.IPAddress,
		"port_range", fmt.Sprintf("%d-%d", slot.PortStart, slot.PortEnd),
		"ssh_key", sshPubPath)

	return vm, nil
}

// Start starts the VM
func (vm *VM) Start() error {
	slog.Info("Starting VM")

	// Start passt first
	if err := vm.PasstManager.Start(); err != nil {
		return fmt.Errorf("failed to start passt: %w", err)
	}

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
		KernelPath:  images.KernelPath,
		InitrdPath:  images.InitramfsPath,
		RootfsPath:  ephemeralDisk,
		Memory:      2048, // 2GB
		CPUs:        4,
		PasstSocket: vm.PasstManager.SocketPath,
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

	// Stop passt
	if vm.PasstManager != nil {
		if err := vm.PasstManager.Stop(); err != nil {
			slog.Warn("Failed to stop passt", "error", err)
		}
	}

	// Release VM slot
	if vm.SlotNumber > 0 {
		if err := ReleaseVMSlot(vm.SlotNumber); err != nil {
			slog.Warn("Failed to release VM slot", "slot", vm.SlotNumber, "error", err)
		}
	}

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
	fmt.Printf("[homura vm] SSH: ssh -p %d root@%s\n", vm.PortStart, vm.IPAddress)
	fmt.Printf("[homura vm] Slot: %d (IP: %s, Ports: %d-%d)\n",
		vm.SlotNumber, vm.IPAddress, vm.PortStart, vm.PortEnd)
	fmt.Println("[homura vm] Network: passt")
	fmt.Println("[homura vm] Press Ctrl+C to stop")
	fmt.Println()
}
