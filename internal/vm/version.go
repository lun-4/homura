package vm

// VMImplementationVersion tracks the homura VM implementation version.
// This version is included in the cache directory path.
// Increment this when:
// - Changing the init script (resources/init)
// - Changing the Dockerfile (resources/Dockerfile)
// - Changing kernel modules loaded in initramfs
// - Changing rootfs build process
// - Adding new VM features that require image rebuild
//
// Version History:
// 1 - Initial implementation with passt networking
// 2 - Add ~/.local/bin to PATH for all shells
// 3 - Add 9p filesystem passthrough support
// 4 - Add custom Dockerfile support with content-based image tagging
// 5 - Replace kernel v9fs with FUSE 9pfuse driver, add complete filesystem operations
// 6 - Add test-fs filesystem validation tool
// 7 - Auto-symlink host Claude config (~/.claude.json, ~/.claude/) into VM
// 8 - Add VM-specific CLAUDE.md injection via /etc/homura/claude-config
// 9 - Increase RAM to 4GB, add 1GB swap file on boot
// 10 - Fix race condition: wait for 9pfuse to serve claude config before symlinking
// 11 - Fix claude code temp files so that symlink actually works
// ...
// 15 - Remove rc_sys="lxc" to fix sysfs/procfs/hostname not starting at boot
// 16 - Force rc_sys="", remove Docker markers, disable TTY getty spam
// 17 - Add backup hostname setting in 9pmount.start script
// 18 - Write /etc/hostname directly in build.go (Docker export loses it)
// 19 - Add virtiofs support as alternative to 9p (fsmount.start now handles both modes)
// 20 - Update 9pvm-request to support virtiofs mode and display deny reasons
// 21 - 9pvm-request: require absolute paths, strip /mnt/host prefix, add -h help
// 22 - Update virtiofsd binary
// 23 - Increase file descriptor limit to 524288 (default 1024 too low)
const VMImplementationVersion = 23
