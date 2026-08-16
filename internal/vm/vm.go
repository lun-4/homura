package vm

import (
	"errors"
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

// MinVirtiofsFileDescriptors is the minimum file descriptor hard limit required for virtiofsd
// virtiofsd tries to set its limit to 1,000,000 and will warn if it can't
const MinVirtiofsFileDescriptors = 100000

// checkFileDescriptorLimit checks if the current file descriptor hard limit is sufficient for virtiofsd
func checkFileDescriptorLimit(minRequired uint64) error {
	var rlimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlimit); err != nil {
		return fmt.Errorf("failed to get file descriptor limit: %w", err)
	}

	if rlimit.Max < minRequired {
		return fmt.Errorf("file descriptor hard limit too low for virtiofsd: %d (need at least %d)\n\n"+
			"virtiofsd requires a high file descriptor limit. To fix this:\n\n"+
			"  Temporary (current session):\n"+
			"    sudo prlimit --pid $$ --nofile=%d:%d\n\n"+
			"  Permanent (add to /etc/security/limits.conf):\n"+
			"    *  hard  nofile  %d\n"+
			"    *  soft  nofile  %d\n\n"+
			"  Then log out and back in, or start a new shell.",
			rlimit.Max, minRequired, minRequired, minRequired, minRequired, minRequired)
	}

	return nil
}

// resolveStateDir resolves the base directory for per-VM state (ephemeral
// disk, sockets). Precedence: explicit stateDirBase arg, then
// HOMURA_VM_STATE_DIR env var, then vm.json stateDir, then the default
// <cacheRoot>/state (which follows a relocated cacheDir). It returns whether
// the default was applied.
func resolveStateDir(stateDirBase, cacheRoot string, vmConfig *VMConfig) (string, bool) {
	base := stateDirBase
	if base == "" {
		base = os.Getenv("HOMURA_VM_STATE_DIR")
	}
	if base == "" {
		base = vmConfig.GetStateDir()
	}
	if base == "" {
		return filepath.Join(cacheRoot, "state"), true
	}
	return base, false
}

// rootfsSizeBytes parses EphemeralDiskSize ("50G") to bytes using semantic
// GiB (G = 1024^3 = 1073741824), matching truncate -s / resize2fs.
func rootfsSizeBytes() (uint64, error) {
	s := strings.ToUpper(strings.TrimSpace(EphemeralDiskSize))
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "G"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult = 1024 * 1024
		s = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult = 1024
		s = strings.TrimSuffix(s, "K")
	}
	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return val * mult, nil
}

// checkStateDirSpace verifies that the filesystem backing dir has at least
// minBytes of free space (using Bavail, so the root-reserved block shortfall
// is accounted for). Returns a descriptive error directing the user to a
// different stateDir when space is insufficient.
func checkStateDirSpace(dir string, minBytes uint64) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return fmt.Errorf("failed to statfs state directory %s: %w", dir, err)
	}
	avail := st.Bavail * uint64(st.Bsize)
	if avail < minBytes {
		return fmt.Errorf(
			"default VM state directory %s lacks ~%s of free space (available %d bytes, need %d bytes for the ephemeral rootfs)\n"+
				"Configure a different stateDir (preferably disk-backed) in ~/.config/homura/vm.json or via HOMURA_VM_STATE_DIR",
			dir, EphemeralDiskSize, avail, minBytes)
	}
	return nil
}

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

// NewVM creates a new VM instance with detected configuration.
// workDir is the directory exposed to the guest (9p/virtiofs share root).
// stateDirBase, when non-empty, overrides the HOMURA_VM_STATE_DIR env var /
// vm.json stateDir resolution for where per-VM state (sockets, ephemeral
// disk) lives; "" keeps that existing resolution.
func NewVM(shareMode ShareMode, workDir string, stateDirBase string) (*VM, error) {
	if shareMode == "" {
		shareMode = ShareModeVirtioFS
	}
	slog.Info("Initializing new VM instance", "share_mode", shareMode, "workdir", workDir)
	newVMStart := time.Now()

	// Check if passt is available
	if !IsPasstAvailable() {
		return nil, fmt.Errorf("passt is required but not found in PATH\n\n" +
			"homura vm requires passt for networking. Please install it:\n" +
			"  • Debian/Ubuntu: apt install passt\n" +
			"  • Arch Linux: pacman -S passt\n" +
			"  • Fedora: dnf install passt\n\n" +
			"For more info: https://passt.top/")
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

	// Get cache root (honors optional cacheDir override in vm.json)
	cacheRoot, err := CacheDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get cache directory: %w", err)
	}

	// Get extra paths from config
	var extraPaths []PathSpec
	if vmConfig != nil {
		extraPaths = vmConfig.GetAllowPaths()
	}

	// Create per-VM state directory. Defaults to <cacheRoot>/state (which
	// follows a relocated cacheDir), but can be pointed at other persistent
	// storage (the ephemeral disk lives here, and on tmpfs every block the
	// guest writes becomes resident RAM).
	baseStateDir, defaulted := resolveStateDir(stateDirBase, cacheRoot, vmConfig)
	if baseStateDir != "" {
		if err := os.MkdirAll(baseStateDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create state base directory %s: %w", baseStateDir, err)
		}
	}
	stateDir, err := os.MkdirTemp(baseStateDir, "homura-vm-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	// When the default state dir was used (user configured nothing), fail fast
	// if it lacks ~rootfs-size free, so the VM doesn't boot just to fill the
	// disk and stall. An explicit stateDir is the user's responsibility.
	if defaulted {
		minBytes, err := rootfsSizeBytes()
		if err != nil {
			os.RemoveAll(stateDir)
			return nil, fmt.Errorf("failed to parse rootfs size: %w", err)
		}
		if err := checkStateDirSpace(stateDir, minBytes); err != nil {
			os.RemoveAll(stateDir)
			return nil, err
		}
	}

	// Unix socket paths (sun_path) are limited to ~108 bytes; passt.sock and
	// virtiofs.sock live in stateDir, so fail early if the path is too deep
	if longest := filepath.Join(stateDir, "virtiofs.sock"); len(longest) > 104 {
		os.RemoveAll(stateDir)
		return nil, fmt.Errorf("state directory path too long for unix sockets (%d > 104 chars): %s\n"+
			"Configure a shorter stateDir in ~/.config/homura/vm.json", len(longest), longest)
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
	// ConsoleSocket/ConsoleLog stay "" here - NewVM doesn't know yet whether
	// Start will run foreground or daemon-mode (that's decided by the
	// StartOptions passed to Start, which happens after allocation). In daemon
	// mode Start() calls UpdateSlotConsole() to populate them once the paths
	// are known. Empty is the correct value for the foreground caller, and is
	// how vm attach/vm stop distinguish fg-owned rows.

	// Variables for the VM instance
	var ninepCmd *exec.Cmd
	var ninepControlSock string
	var ninepToken string
	var ninepControlPort int
	var ninepPort int
	var virtiofsManager *VirtiofsManager

	// Start filesystem sharing daemon based on mode
	fsDaemonStart := time.Now()
	switch shareMode {
	case ShareModeVirtioFS:
		// Check file descriptor limit before starting virtiofsd
		if err := checkFileDescriptorLimit(MinVirtiofsFileDescriptors); err != nil {
			os.RemoveAll(stateDir)
			return nil, err
		}

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

		ninepBinary := filepath.Join(cacheRoot, "bin", "9passthrough")
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
		claudeLock := filepath.Join(homeDir, ".claude.lock")
		ninepArgs = append(ninepArgs, claudeLock)
		addedPaths[claudeLock] = false
		slog.Info("Auto-exposing Claude lock", "path", claudeLock)

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
		if ChildProcAttr != nil {
			ninepCmd.SysProcAttr = ChildProcAttr
		}
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
	fsDaemonTook := time.Since(fsDaemonStart)

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
	slotAllocStart := time.Now()
	slot, err := AllocateVMSlot(slotParams)
	if err != nil {
		cleanupOnError()
		return nil, fmt.Errorf("failed to allocate VM slot: %w", err)
	}
	slotAllocTook := time.Since(slotAllocStart)

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
		"share_mode", shareMode,
		"fs_daemon_took", fsDaemonTook,
		"slot_alloc_took", slotAllocTook,
		"total_took", time.Since(newVMStart))

	return vm, nil
}

// StartOptions controls how Start wires up the QEMU process.
type StartOptions struct {
	Foreground    bool   // -serial stdio + os.Stdin/Stdout/Stderr wiring (today's behavior)
	ConsoleSocket string // unix socket path for the serial chardev (daemon mode)
	ConsoleLog    string // QEMU chardev logfile path (daemon mode)
}

// Start starts the VM
func (vm *VM) Start(opts StartOptions) error {
	slog.Info("Starting VM", "foreground", opts.Foreground)
	startBegin := time.Now()

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
	phaseStart := time.Now()
	if err := vm.PasstManager.Start(); err != nil {
		return fmt.Errorf("failed to start passt: %w", err)
	}
	passtTook := time.Since(phaseStart)

	// Ensure images are downloaded and built
	phaseStart = time.Now()
	images, err := EnsureImages(vm.SSHPubPath)
	if err != nil {
		return fmt.Errorf("failed to ensure images: %w", err)
	}
	vm.Images = images
	imagesTook := time.Since(phaseStart)

	// Create ephemeral disk
	phaseStart = time.Now()
	ephemeralDisk := filepath.Join(vm.StateDir, "rootfs-ephemeral.qcow2")
	if err := CreateEphemeralDisk(images.RootfsPath, ephemeralDisk); err != nil {
		return fmt.Errorf("failed to create ephemeral disk: %w", err)
	}
	vm.EphemeralDisk = ephemeralDisk
	diskTook := time.Since(phaseStart)

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
		HostIP:      vm.IPAddress,
		SSHPort:     vm.PortStart,
		PortStart:   vm.PortStart,
		PortEnd:     vm.PortEnd,
		SlotNumber:  vm.SlotNumber,
	}

	if !opts.Foreground {
		cfg.ConsoleSocket = opts.ConsoleSocket
		cfg.ConsoleLog = opts.ConsoleLog
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
	if opts.Foreground {
		vm.QEMUCmd.Stdin = os.Stdin
		vm.QEMUCmd.Stdout = os.Stdout
		vm.QEMUCmd.Stderr = os.Stderr
	} else {
		vm.QEMUCmd.Stdin = nil
		vm.QEMUCmd.Stdout = ChildOutput
		vm.QEMUCmd.Stderr = ChildOutput
		// Daemon-spawned QEMU dies with the daemon rather than being orphaned.
		vm.QEMUCmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	}

	// Start QEMU
	slog.Info("Launching QEMU")
	if err := vm.QEMUCmd.Start(); err != nil {
		return fmt.Errorf("failed to start QEMU: %w", err)
	}

	slog.Info("QEMU process started", "pid", vm.QEMUCmd.Process.Pid,
		"passt_took", passtTook,
		"images_took", imagesTook,
		"disk_took", diskTook,
		"total_took", time.Since(startBegin))

	// Record the QEMU PID now that it's known (AllocateVMSlot ran before QEMU
	// was launched, so it couldn't have this value); harmless in fg mode.
	if vm.SlotNumber > 0 {
		if err := UpdateSlotQEMUPid(vm.SlotNumber, vm.QEMUCmd.Process.Pid); err != nil {
			slog.Warn("Failed to record QEMU pid", "slot", vm.SlotNumber, "error", err)
		}
		// In daemon mode, record the console paths so vm attach/vm stop can
		// find them via the DB. Skipped in fg mode (paths are empty there).
		if !opts.Foreground {
			if err := UpdateSlotConsole(vm.SlotNumber, opts.ConsoleSocket, opts.ConsoleLog); err != nil {
				slog.Warn("Failed to record console paths", "slot", vm.SlotNumber, "error", err)
			}
		}
	}

	// Display connection info to the user in foreground mode. In daemon mode
	// the RPC result carries these facts back to the client instead, and the
	// daemon must not print user-facing text to its own stdout.
	if opts.Foreground {
		vm.DisplayConnectionInfo()
	}

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

// Shutdown signals the QEMU process to stop: SIGTERM, then SIGKILL if it
// hasn't exited within timeout. It only signals - it does not call Wait() -
// because in daemon mode a supervision goroutine owns Wait() and reaping
// here would race it. The caller is responsible for reaping the process.
func (vm *VM) Shutdown(timeout time.Duration) error {
	if vm.QEMUCmd == nil || vm.QEMUCmd.Process == nil {
		return fmt.Errorf("VM not running")
	}
	proc := vm.QEMUCmd.Process

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Signal 0 failing means the process is gone.
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	slog.Warn("VM did not exit after SIGTERM, sending SIGKILL", "pid", proc.Pid)
	if err := proc.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("failed to send SIGKILL: %w", err)
	}
	return nil
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

// ConnectionInfo holds the facts a user (or a daemon RPC client) needs to
// connect to a running VM.
type ConnectionInfo struct {
	IPAddress  string
	SSHPort    int
	PortStart  int
	PortEnd    int
	SlotNumber int
	ShareMode  ShareMode
	WorkDir    string
}

// ConnectionInfo returns the current connection facts for this VM.
func (vm *VM) ConnectionInfo() ConnectionInfo {
	return ConnectionInfo{
		IPAddress:  vm.IPAddress,
		SSHPort:    vm.PortStart,
		PortStart:  vm.PortStart,
		PortEnd:    vm.PortEnd,
		SlotNumber: vm.SlotNumber,
		ShareMode:  vm.ShareMode,
		WorkDir:    vm.WorkDir,
	}
}

// DisplayConnectionInfo prints connection information to the user
func (vm *VM) DisplayConnectionInfo() {
	info := vm.ConnectionInfo()
	fmt.Println()
	fmt.Println("[homura vm] Starting microvm...")
	fmt.Printf("[homura vm] SSH: ssh -p %d root@%s\n", info.SSHPort, info.IPAddress)
	fmt.Printf("[homura vm] Slot: %d (IP: %s, Ports: %d-%d)\n",
		info.SlotNumber, info.IPAddress, info.PortStart, info.PortEnd)
	fmt.Println("[homura vm] Network: passt")
	fmt.Printf("[homura vm] Filesystem: /mnt/host (%s, exposed: %s)\n", info.ShareMode, info.WorkDir)
	fmt.Println("[homura vm] Request paths from VM: 9pvm-request /path/to/expose")
	fmt.Println("[homura vm] Control from host: homura 9p <command>")
	fmt.Println("[homura vm] Press Ctrl+C to stop")
	fmt.Println()
}
