package vm

import (
	"fmt"
	"strconv"
)

// QEMUConfig contains configuration for launching QEMU
type QEMUConfig struct {
	KernelPath  string
	InitrdPath  string
	RootfsPath  string    // Path to ephemeral ext4 disk
	Memory      int       // MB
	CPUs        int
	PasstSocket string    // Passt Unix socket path
	HostHomeDir string    // Host user's home directory
	ShareMode   ShareMode // Filesystem sharing mode

	// 9p fields (used when ShareMode == "9p")
	NinePToken       string // 9p authentication token
	NinePControlPort int    // 9p control port
	NinePPort        int    // 9p listen port (auto-allocated)

	// virtiofs fields (used when ShareMode == "virtiofs")
	VirtiofsSocket  string // vhost-user socket for QEMU
	VirtiofsVMToken string // Token for VM to authenticate with virtiofsd
	VirtiofsVMPort  int    // Port for VM to send requests to virtiofsd

	// VM networking info (passed to guest for CLAUDE.md injection)
	HostIP     string // Host-side IP (e.g., 127.0.0.1)
	SSHPort    int    // Host-side SSH port
	PortStart  int    // First port in range
	PortEnd    int    // Last port in range
	SlotNumber int    // VM slot number

	// Serial console (daemon mode). When ConsoleSocket is empty, the serial
	// console goes to stdio (today's foreground behavior).
	ConsoleSocket string // unix socket path for the serial chardev
	ConsoleLog    string // QEMU chardev logfile path
}

// BuildQEMUArgs builds the argument list for launching QEMU with q35 machine
func BuildQEMUArgs(cfg *QEMUConfig) []string {
	args := []string{
		// Use q35 machine type (modern, supports PCI)
		"-M", "q35",
		"-enable-kvm",
		"-cpu", "host",

		// Memory - must use memfd backend with share=on for vhost-user
		"-m", fmt.Sprintf("%dM", cfg.Memory),
		"-object", fmt.Sprintf("memory-backend-memfd,id=mem,size=%dM,share=on", cfg.Memory),
		"-numa", "node,memdev=mem",

		"-smp", strconv.Itoa(cfg.CPUs),

		// Direct kernel boot with initrd
		"-kernel", cfg.KernelPath,
		"-initrd", cfg.InitrdPath,
		"-append", buildKernelCmdline(cfg),

		// No defaults, no graphics
		"-nodefaults",
		"-no-user-config",
		"-nographic",
	}

	// Serial console: a unix-socket chardev in daemon mode (QEMU persists
	// output to logfile itself, whether or not anything is connected), or
	// stdio for today's foreground behavior.
	if cfg.ConsoleSocket != "" {
		args = append(args,
			"-chardev", fmt.Sprintf("socket,id=ser0,path=%s,server=on,wait=off,logfile=%s,logappend=on",
				cfg.ConsoleSocket, cfg.ConsoleLog),
			"-serial", "chardev:ser0")
	} else {
		args = append(args, "-serial", "stdio")
	}

	args = append(args,
		// Rootfs as virtio-blk device (PCI transport for microvm)
		"-drive", fmt.Sprintf("id=root,file=%s,format=raw,if=none", cfg.RootfsPath),
		"-device", "virtio-blk-pci,drive=root",

		// Passt networking via Unix socket (PCI transport for microvm)
		"-netdev", fmt.Sprintf("stream,id=net0,addr.type=unix,addr.path=%s", cfg.PasstSocket),
		"-device", "virtio-net-pci,netdev=net0",
	)

	// Add virtiofs device if using virtiofs mode
	if cfg.ShareMode == ShareModeVirtioFS && cfg.VirtiofsSocket != "" {
		args = append(args,
			"-chardev", fmt.Sprintf("socket,id=virtiofs0,path=%s", cfg.VirtiofsSocket),
			"-device", "vhost-user-fs-pci,queue-size=1024,chardev=virtiofs0,tag=hostfs",
		)
	}

	return args
}

// buildKernelCmdline builds the kernel command line string
func buildKernelCmdline(cfg *QEMUConfig) string {
	// Kernel parameters for microvm boot
	cmdline := "earlyprintk=ttyS0 console=ttyS0 root=/dev/vda rootfstype=ext4 rw"

	// Add share-mode-specific params
	switch cfg.ShareMode {
	case ShareModeVirtioFS:
		// virtiofs mode params
		if cfg.VirtiofsVMToken != "" {
			cmdline += fmt.Sprintf(" virtiofs.token=%s virtiofs.port=%d",
				cfg.VirtiofsVMToken, cfg.VirtiofsVMPort)
		}
	default: // ShareMode9P
		// 9p mode params
		if cfg.NinePToken != "" {
			cmdline += fmt.Sprintf(" p9.token=%s p9.port=%d p9.listenport=%d",
				cfg.NinePToken, cfg.NinePControlPort, cfg.NinePPort)
		}
	}

	// Add host home directory for Claude config symlinks
	if cfg.HostHomeDir != "" {
		cmdline += fmt.Sprintf(" host.home=%s", cfg.HostHomeDir)
	}

	// Add VM networking info (used by fsmount.sh to inject into CLAUDE.md)
	if cfg.HostIP != "" {
		cmdline += fmt.Sprintf(" vm.hostip=%s vm.sshport=%d vm.portstart=%d vm.portend=%d vm.slot=%d",
			cfg.HostIP, cfg.SSHPort, cfg.PortStart, cfg.PortEnd, cfg.SlotNumber)
	}

	return cmdline
}
