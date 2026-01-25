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
const VMImplementationVersion = 1
