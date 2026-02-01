package vm

import (
	"fmt"
	"strconv"
)

// QEMUConfig contains configuration for launching QEMU
type QEMUConfig struct {
	KernelPath       string
	InitrdPath       string
	RootfsPath       string // Path to ephemeral ext4 disk
	Memory           int    // MB
	CPUs             int
	PasstSocket      string // Passt Unix socket path
	NinePToken       string // 9p authentication token
	NinePControlPort int    // 9p control port
	NinePPort        int    // 9p listen port (auto-allocated)
	HostHomeDir      string // Host user's home directory
}

// BuildQEMUArgs builds the argument list for launching QEMU with q35 machine
func BuildQEMUArgs(cfg *QEMUConfig) []string {
	args := []string{
		// Use q35 machine type (modern, supports PCI)
		"-M", "q35",
		"-enable-kvm",
		"-cpu", "host",

		// Memory and CPUs
		"-m", fmt.Sprintf("%dM", cfg.Memory),
		"-smp", strconv.Itoa(cfg.CPUs),

		// Direct kernel boot with initrd
		"-kernel", cfg.KernelPath,
		"-initrd", cfg.InitrdPath,
		"-append", buildKernelCmdline(cfg),

		// No defaults, no graphics
		"-nodefaults",
		"-no-user-config",
		"-nographic",

		// Serial console to stdio
		"-serial", "stdio",

		// Rootfs as virtio-blk device (PCI transport for microvm)
		"-drive", fmt.Sprintf("id=root,file=%s,format=raw,if=none", cfg.RootfsPath),
		"-device", "virtio-blk-pci,drive=root",

		// Passt networking via Unix socket (PCI transport for microvm)
		"-netdev", fmt.Sprintf("stream,id=net0,addr.type=unix,addr.path=%s", cfg.PasstSocket),
		"-device", "virtio-net-pci,netdev=net0",
	}

	return args
}

// buildKernelCmdline builds the kernel command line string
func buildKernelCmdline(cfg *QEMUConfig) string {
	// Kernel parameters for microvm boot
	cmdline := "earlyprintk=ttyS0 console=ttyS0 root=/dev/vda rootfstype=ext4 rw"

	// Add 9p params
	if cfg.NinePToken != "" {
		cmdline += fmt.Sprintf(" p9.token=%s p9.port=%d p9.listenport=%d",
			cfg.NinePToken, cfg.NinePControlPort, cfg.NinePPort)
	}

	// Add host home directory for Claude config symlinks
	if cfg.HostHomeDir != "" {
		cmdline += fmt.Sprintf(" host.home=%s", cfg.HostHomeDir)
	}

	return cmdline
}
