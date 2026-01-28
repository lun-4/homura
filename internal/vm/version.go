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
const VMImplementationVersion = 6
