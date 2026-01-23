package vm

import (
	"fmt"
	"strconv"
)

// QEMUConfig contains configuration for launching QEMU
type QEMUConfig struct {
	KernelPath   string
	InitrdPath   string
	RootfsPath   string // Path to ephemeral ext4 disk
	Memory       int    // MB
	CPUs         int
	PasstSocket  string // Passt Unix socket path
}

// BuildQEMUArgs builds the argument list for launching QEMU microvm
func BuildQEMUArgs(cfg *QEMUConfig) []string {
	args := []string{
		// Use microvm machine type for fast boot
		"-M", "microvm",
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

		// Rootfs as virtio-blk device
		"-drive", fmt.Sprintf("id=root,file=%s,format=raw,if=none", cfg.RootfsPath),
		"-device", "virtio-blk-device,drive=root",

		// Passt networking via Unix socket
		"-netdev", fmt.Sprintf("stream,id=net0,addr.type=unix,addr.path=%s", cfg.PasstSocket),
		"-device", "virtio-net-device,netdev=net0",
	}

	return args
}

// buildKernelCmdline builds the kernel command line string
func buildKernelCmdline(cfg *QEMUConfig) string {
	// Kernel parameters for microvm boot
	cmdline := "earlyprintk=ttyS0 console=ttyS0 root=/dev/vda rootfstype=ext4 rw"
	return cmdline
}
