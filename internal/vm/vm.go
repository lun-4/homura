package vm

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// VM represents a running homura VM instance
type VM struct {
	WorkDir       string        // Current working directory
	SSHPubPath    string        // Path to user's SSH public key
	QEMUCmd       *exec.Cmd     // QEMU process
	StateDir      string        // Temporary state directory
	Images        *ImagePaths
	EphemeralDisk string        // Path to ephemeral rootfs disk
	HostHomeDir   string        // Host user's home directory

	// Passt networking fields
	SlotNumber   int            // Sequential slot (1-254)
	IPAddress    string         // 127.0.0.X
	PortStart    int            // First port in range (e.g., 10000)
	PortEnd      int            // Last port in range (e.g., 10009)
	PasstManager *PasstManager  // Passt daemon manager

	// Filesystem sharing mode
	ShareMode ShareMode // "9p" or "virtiofs"

	// 9p filesystem fields (used when ShareMode == "9p")
	NinePProcess      *exec.Cmd
	NinePControlSock  string
	NinePToken        string
	NinePControlPort  int
	NinePPort         int // Actual 9p listen port (auto-allocated)

	// virtiofs fields (used when ShareMode == "virtiofs")
	VirtiofsManager *VirtiofsManager
}

// NewVM creates a new VM instance with detected configuration
func NewVM(shareMode ShareMode) (*VM, error) {
	if shareMode == "" {
		shareMode = ShareMode9P
	}
	slog.Info("Initializing new VM instance", "share_mode", shareMode)

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

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	// Load persistent VM config
	vmConfig, err := LoadVMConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load VM config: %w", err)
	}

	// Get extra paths from config
	var extraPaths []PathSpec
	if vmConfig != nil {
		extraPaths = vmConfig.GetAllowPaths()
	}

	// Create temporary state directory
	stateDir, err := os.MkdirTemp("", "homura-vm-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	// Construct passt socket path (slot allocation comes later)
	socketPath := filepath.Join(stateDir, "passt.sock")

	// Try common SSH key locations (do this early so we can fail fast)
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
		os.RemoveAll(stateDir)
		return nil, fmt.Errorf("no SSH public key found in ~/.ssh/ (tried: id_ed25519.pub, id_rsa.pub, id_ecdsa.pub)")
	}

	// Pre-allocate slot number for virtiofsd (it needs the slot number for --vm-name)
	peekedSlot, err := PeekNextSlotNumber()
	if err != nil {
		os.RemoveAll(stateDir)
		return nil, fmt.Errorf("failed to peek next VM slot: %w", err)
	}
	slog.Info("Pre-allocated slot number", "slot", peekedSlot)

	// Variables for slot allocation params
	var slotParams VMSlotParams
	slotParams.SocketPath = socketPath
	slotParams.WorkingDir = workDir
	slotParams.ShareMode = string(shareMode)

	// Variables for the VM instance
	var ninepCmd *exec.Cmd
	var ninepControlSock string
	var ninepToken string
	var ninepControlPort int
	var ninepPort int
	var virtiofsManager *VirtiofsManager

	// Start filesystem sharing daemon based on mode
	switch shareMode {
	case ShareModeVirtioFS:
		// Start virtiofsd
		slog.Info("Starting virtiofsd", "workdir", workDir)

		virtiofsManager, err = NewVirtiofsManager(stateDir, homeDir, workDir, peekedSlot, extraPaths)
		if err != nil {
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("failed to create virtiofs manager: %w", err)
		}

		if err := virtiofsManager.Start(); err != nil {
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("failed to start virtiofsd: %w", err)
		}

		slotParams.VirtiofsPID = virtiofsManager.GetPID()
		slotParams.VirtiofsSocket = virtiofsManager.SocketPath
		slotParams.VirtiofsAdminPort = virtiofsManager.AdminPort
		slotParams.VirtiofsVMPort = virtiofsManager.VMPort

	default: // ShareMode9P
		// Start 9passthrough server
		slog.Info("Starting 9passthrough server", "workdir", workDir)

		ninepBinary := filepath.Join(homeDir, ".cache", "homura", "bin", "9passthrough")
		if _, err := os.Stat(ninepBinary); os.IsNotExist(err) {
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("9passthrough binary not found at %s (run 'make 9p' to build)", ninepBinary)
		}

		// Build args: collect builtin paths first, then add non-duplicate configured paths
		// addedPaths tracks all paths sent to 9passthrough (true = read-only, false = read-write)
		addedPaths := make(map[string]bool)

		// workDir is always first and read-write
		ninepArgs := []string{workDir}
		addedPaths[workDir] = false

		// Auto-expose Claude config files (read-write to allow updates)
		claudeJson := filepath.Join(homeDir, ".claude.json")
		claudeDir := filepath.Join(homeDir, ".claude")
		if _, err := os.Stat(claudeJson); err == nil {
			ninepArgs = append(ninepArgs, claudeJson)
			addedPaths[claudeJson] = false
			slog.Info("Auto-exposing Claude config", "path", claudeJson)
		}
		if _, err := os.Stat(claudeDir); err == nil {
			ninepArgs = append(ninepArgs, claudeDir)
			addedPaths[claudeDir] = false
			slog.Info("Auto-exposing Claude config", "path", claudeDir)
		}

		// Auto-expose VM CLAUDE.md customizations (read-only)
		vmClaudeMd := filepath.Join(homeDir, ".config", "homura", "CLAUDE.md")
		if _, err := os.Stat(vmClaudeMd); err == nil {
			ninepArgs = append(ninepArgs, vmClaudeMd+":ro")
			addedPaths[vmClaudeMd] = true
			slog.Info("Auto-exposing VM CLAUDE.md (read-only)", "path", vmClaudeMd)
		}

		// Add configured paths from vm.json, skipping any duplicates
		for _, spec := range extraPaths {
			if _, alreadyAdded := addedPaths[spec.Path]; alreadyAdded {
				slog.Info("Skipping configured path (already added)", "path", spec.Path)
				continue
			}
			ninepArgs = append(ninepArgs, spec.FormatPathArg())
			addedPaths[spec.Path] = spec.ReadOnly
			slog.Info("Adding configured path", "path", spec.Path, "readonly", spec.ReadOnly)
		}

		ninepCmd = exec.Command(ninepBinary, ninepArgs...)
		if err := ninepCmd.Start(); err != nil {
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("failed to start 9passthrough: %w", err)
		}

		// Wait for token file with polling
		tokenFile := fmt.Sprintf("/tmp/9p-token-%d", ninepCmd.Process.Pid)
		var tokenData []byte
		deadline := time.Now().Add(1 * time.Second)
		for time.Now().Before(deadline) {
			var readErr error
			tokenData, readErr = os.ReadFile(tokenFile)
			if readErr == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if tokenData == nil {
			ninepCmd.Process.Kill()
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("failed to read 9p token file: timed out after 1s")
		}

		lines := strings.Split(strings.TrimSpace(string(tokenData)), "\n")
		if len(lines) < 3 {
			ninepCmd.Process.Kill()
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("invalid 9p token file format (expected 3 lines, got %d)", len(lines))
		}

		ninepToken = lines[0]
		ninepControlPort, err = strconv.Atoi(lines[1])
		if err != nil {
			ninepCmd.Process.Kill()
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("invalid 9p control port: %w", err)
		}
		ninepPort, err = strconv.Atoi(lines[2])
		if err != nil {
			ninepCmd.Process.Kill()
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("invalid 9p listen port: %w", err)
		}
		ninepControlSock = fmt.Sprintf("/tmp/9p-control-%d.sock", ninepCmd.Process.Pid)

		slog.Info("9passthrough started", "pid", ninepCmd.Process.Pid, "control_port", ninepControlPort, "9p_port", ninepPort)

		slotParams.NinePPID = ninepCmd.Process.Pid
		slotParams.NinePSocket = ninepControlSock
		slotParams.NinePPort = ninepControlPort
	}

	// Helper to clean up on error
	cleanupOnError := func() {
		if ninepCmd != nil && ninepCmd.Process != nil {
			ninepCmd.Process.Kill()
		}
		if virtiofsManager != nil {
			virtiofsManager.Stop()
		}
		os.RemoveAll(stateDir)
	}

	// Allocate VM slot
	slot, err := AllocateVMSlot(slotParams)
	if err != nil {
		cleanupOnError()
		return nil, fmt.Errorf("failed to allocate VM slot: %w", err)
	}

	// Create passt manager
	passtMgr, err := NewPasstManager(stateDir, slot)
	if err != nil {
		cleanupOnError()
		ReleaseVMSlot(slot.SlotNumber)
		return nil, fmt.Errorf("failed to create passt manager: %w", err)
	}

	vm := &VM{
		WorkDir:          workDir,
		SSHPubPath:       sshPubPath,
		StateDir:         stateDir,
		HostHomeDir:      homeDir,
		SlotNumber:       slot.SlotNumber,
		IPAddress:        slot.IPAddress,
		PortStart:        slot.PortStart,
		PortEnd:          slot.PortEnd,
		PasstManager:     passtMgr,
		ShareMode:        shareMode,
		NinePProcess:     ninepCmd,
		NinePControlSock: ninepControlSock,
		NinePToken:       ninepToken,
		NinePControlPort: ninepControlPort,
		NinePPort:        ninepPort,
		VirtiofsManager:  virtiofsManager,
	}

	slog.Info("VM instance initialized",
		"workdir", workDir,
		"slot", slot.SlotNumber,
		"ip", slot.IPAddress,
		"port_range", fmt.Sprintf("%d-%d", slot.PortStart, slot.PortEnd),
		"ssh_key", sshPubPath,
		"share_mode", shareMode)

	return vm, nil
}

// Start starts the VM
func (vm *VM) Start() error {
	slog.Info("Starting VM")

	// Run pre-start snapshot if configured
	vmConfig, err := LoadVMConfig()
	if err != nil {
		slog.Warn("Failed to load VM config for snapshot", "error", err)
	} else if vmConfig != nil && len(vmConfig.GetSnapshotPaths()) > 0 {
		slog.Info("Creating pre-start snapshot")
		if err := CreateSnapshot(vmConfig.GetSnapshotPaths(), vmConfig.GetMaxSnapshots()); err != nil {
			slog.Warn("Failed to create snapshot", "error", err)
			// Continue with VM start even if snapshot fails
		}
	}

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
		Memory:      4096, // 4GB
		CPUs:        4,
		PasstSocket: vm.PasstManager.SocketPath,
		HostHomeDir: vm.HostHomeDir,
		ShareMode:   vm.ShareMode,
	}

	// Set share-mode-specific config
	switch vm.ShareMode {
	case ShareModeVirtioFS:
		cfg.VirtiofsSocket = vm.VirtiofsManager.SocketPath
		cfg.VirtiofsVMToken = vm.VirtiofsManager.VMToken
		cfg.VirtiofsVMPort = vm.VirtiofsManager.VMPort
	default: // ShareMode9P
		cfg.NinePToken = vm.NinePToken
		cfg.NinePControlPort = vm.NinePControlPort
		cfg.NinePPort = vm.NinePPort
	}

	// Build QEMU command
	args := BuildQEMUArgs(cfg)

	// Debug: print full QEMU command
	slog.Info("QEMU command", "cmd", fmt.Sprintf("qemu-system-x86_64 %s", strings.Join(args, " ")))

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
	slog.Info("Cleaning up VM state", "state_dir", vm.StateDir, "share_mode", vm.ShareMode)

	// Stop filesystem sharing daemon based on mode
	switch vm.ShareMode {
	case ShareModeVirtioFS:
		// Stop virtiofsd
		if vm.VirtiofsManager != nil {
			slog.Info("Stopping virtiofsd")
			vm.VirtiofsManager.RemoveTokenFile()
			if err := vm.VirtiofsManager.Stop(); err != nil {
				slog.Warn("Failed to stop virtiofsd", "error", err)
			}
		}

	default: // ShareMode9P
		// Stop 9passthrough
		if vm.NinePProcess != nil {
			slog.Info("Stopping 9passthrough", "pid", vm.NinePProcess.Process.Pid)
			if err := vm.NinePProcess.Process.Kill(); err != nil {
				slog.Warn("Failed to kill 9passthrough", "error", err)
			}
			vm.NinePProcess.Wait() // Reap zombie

			// Clean up token file
			tokenFile := fmt.Sprintf("/tmp/9p-token-%d", vm.NinePProcess.Process.Pid)
			os.Remove(tokenFile)
		}
	}

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
	fmt.Printf("[homura vm] Filesystem: /mnt/host (%s, exposed: %s)\n", vm.ShareMode, vm.WorkDir)
	fmt.Println("[homura vm] Request paths from VM: 9pvm-request /path/to/expose")
	fmt.Println("[homura vm] Control from host: homura 9p <command>")
	fmt.Println("[homura vm] Press Ctrl+C to stop")
	fmt.Println()
}
