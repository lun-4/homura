package vm

import (
	_ "embed"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	alpineVersion = "3.21"
	alpineRelease = "3.21.3"
	alpineMirror  = "https://dl-cdn.alpinelinux.org"
)

//go:embed resources/Dockerfile
var dockerfileContent string

//go:embed resources/init
var initScriptContent string

// ImagePaths holds paths to all VM images
type ImagePaths struct {
	CacheDir          string
	KernelPath        string
	InitramfsPath     string
	ModloopPath       string
	RootfsPath        string
	SSHHostKeysDir    string
	EphemeralRootfs   string
}

// EnsureImages downloads and builds all required VM images
func EnsureImages(sshPubKeyPath string) (*ImagePaths, error) {
	slog.Info("Ensuring VM images are available")

	// Create cache directory
	cacheDir, err := getCacheDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get cache directory: %w", err)
	}

	// Get stable SSH keys directory (not versioned)
	sshKeysDir, err := getSSHKeysDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get SSH keys directory: %w", err)
	}

	// Check for custom Dockerfile and calculate hash for rootfs filename
	rootfsFilename := "rootfs.ext4"
	customDockerfilePath := filepath.Join(os.Getenv("HOME"), ".config", "homura", "Dockerfile.custom")
	if fileInfo, err := os.Stat(customDockerfilePath); err == nil && !fileInfo.IsDir() {
		customContent, err := os.ReadFile(customDockerfilePath)
		if err == nil {
			hash := md5.Sum(customContent)
			hashPrefix := fmt.Sprintf("%x", hash)[:6]
			rootfsFilename = fmt.Sprintf("rootfs-%s.ext4", hashPrefix)
			slog.Debug("Using custom rootfs filename", "filename", rootfsFilename, "hash", hashPrefix)
		}
	}

	paths := &ImagePaths{
		CacheDir:        cacheDir,
		KernelPath:      filepath.Join(cacheDir, "vmlinuz-virt"),
		InitramfsPath:   filepath.Join(cacheDir, "initramfs-virt"),
		ModloopPath:     filepath.Join(cacheDir, "modloop-virt"),
		RootfsPath:      filepath.Join(cacheDir, rootfsFilename),
		SSHHostKeysDir:  sshKeysDir,
	}

	// Download Alpine components if needed
	if err := downloadAlpineComponents(paths); err != nil {
		return nil, fmt.Errorf("failed to download Alpine components: %w", err)
	}

	// Build custom initramfs if needed
	if err := buildInitramfs(paths); err != nil {
		return nil, fmt.Errorf("failed to build initramfs: %w", err)
	}

	// Generate SSH host keys if needed
	if err := ensureSSHHostKeys(paths); err != nil {
		return nil, fmt.Errorf("failed to ensure SSH host keys: %w", err)
	}

	// Build rootfs if needed
	if err := buildRootfs(paths, sshPubKeyPath); err != nil {
		return nil, fmt.Errorf("failed to build rootfs: %w", err)
	}

	slog.Info("All VM images ready", "cache_dir", cacheDir)
	return paths, nil
}

// getCacheDir returns the cache directory for VM images
func getCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(home, ".cache", "homura", fmt.Sprintf("v%d", VMImplementationVersion), "vm-images", "alpine-"+alpineVersion)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	return cacheDir, nil
}

// getSSHKeysDir returns the stable SSH host keys directory (not versioned)
func getSSHKeysDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	sshKeysDir := filepath.Join(home, ".cache", "homura", "ssh_host_keys")
	if err := os.MkdirAll(sshKeysDir, 0755); err != nil {
		return "", err
	}
	return sshKeysDir, nil
}

// downloadAlpineComponents downloads kernel, initramfs, and modloop from Alpine CDN
func downloadAlpineComponents(paths *ImagePaths) error {
	components := []struct {
		name string
		url  string
		path string
	}{
		{
			name: "kernel",
			url:  fmt.Sprintf("%s/alpine/v%s/releases/x86_64/netboot/vmlinuz-virt", alpineMirror, alpineVersion),
			path: paths.KernelPath,
		},
		{
			name: "initramfs (original)",
			url:  fmt.Sprintf("%s/alpine/v%s/releases/x86_64/netboot/initramfs-virt", alpineMirror, alpineVersion),
			path: paths.ModloopPath + ".orig", // temp name, will be used to build custom initramfs
		},
		{
			name: "modloop",
			url:  fmt.Sprintf("%s/alpine/v%s/releases/x86_64/netboot/modloop-virt", alpineMirror, alpineVersion),
			path: paths.ModloopPath,
		},
	}

	for _, comp := range components {
		if _, err := os.Stat(comp.path); err == nil {
			slog.Debug("Alpine component already exists", "name", comp.name, "path", comp.path)
			continue
		}

		slog.Info("Downloading Alpine component", "name", comp.name, "url", comp.url)
		if err := downloadFile(comp.url, comp.path); err != nil {
			return fmt.Errorf("failed to download %s: %w", comp.name, err)
		}
	}

	return nil
}

// downloadFile downloads a file from a URL to a local path
func downloadFile(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Create temp file
	tmpPath := destPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Download
	if _, err := io.Copy(out, resp.Body); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	return nil
}

// ensureSSHHostKeys generates persistent SSH host keys if they don't exist
func ensureSSHHostKeys(paths *ImagePaths) error {
	if err := os.MkdirAll(paths.SSHHostKeysDir, 0700); err != nil {
		return err
	}

	keyTypes := []struct {
		name string
		args []string
	}{
		{"ssh_host_ed25519_key", []string{"-t", "ed25519", "-N", "", "-q"}},
		{"ssh_host_rsa_key", []string{"-t", "rsa", "-b", "4096", "-N", "", "-q"}},
		{"ssh_host_ecdsa_key", []string{"-t", "ecdsa", "-N", "", "-q"}},
	}

	for _, kt := range keyTypes {
		keyPath := filepath.Join(paths.SSHHostKeysDir, kt.name)
		if _, err := os.Stat(keyPath); err == nil {
			continue // Already exists
		}

		slog.Info("Generating SSH host key", "type", kt.name)
		args := append(kt.args, "-f", keyPath)
		cmd := exec.Command("ssh-keygen", args...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to generate %s: %w", kt.name, err)
		}
	}

	return nil
}

// buildInitramfs creates a custom initramfs with our init script
func buildInitramfs(paths *ImagePaths) error {
	if _, err := os.Stat(paths.InitramfsPath); err == nil {
		slog.Debug("Custom initramfs already exists", "path", paths.InitramfsPath)
		return nil
	}

	slog.Info("Building custom initramfs")

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "homura-initramfs-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	origInitramfsPath := paths.ModloopPath + ".orig"

	// Extract Alpine's initramfs
	slog.Debug("Extracting original initramfs")
	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return err
	}

	cmd := exec.Command("sh", "-c", fmt.Sprintf("gzip -dc %s | cpio -idm 2>/dev/null", origInitramfsPath))
	cmd.Dir = extractDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract initramfs: %w", err)
	}

	// Get kernel version
	modulesDir := filepath.Join(extractDir, "lib", "modules")
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return fmt.Errorf("failed to read modules directory: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no kernel version found in initramfs")
	}
	kver := entries[0].Name()
	slog.Debug("Detected kernel version", "version", kver)

	// Create new initramfs structure
	newDir := filepath.Join(tmpDir, "new_initramfs")
	dirs := []string{
		"bin", "sbin", "lib", "proc", "sys", "dev", "newroot",
		filepath.Join("lib", "modules", kver, "kernel", "fs"),
		filepath.Join("lib", "modules", kver, "kernel", "lib"),
		filepath.Join("lib", "modules", kver, "kernel", "drivers", "net"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(newDir, dir), 0755); err != nil {
			return err
		}
	}

	// Copy busybox
	if err := copyFile(
		filepath.Join(extractDir, "bin", "busybox"),
		filepath.Join(newDir, "bin", "busybox"),
	); err != nil {
		return fmt.Errorf("failed to copy busybox: %w", err)
	}
	if err := os.Chmod(filepath.Join(newDir, "bin", "busybox"), 0755); err != nil {
		return err
	}

	// Create sh symlink
	if err := os.Symlink("busybox", filepath.Join(newDir, "bin", "sh")); err != nil {
		return err
	}
	if err := os.Symlink("../bin/busybox", filepath.Join(newDir, "sbin", "modprobe")); err != nil {
		return err
	}

	// Copy musl libc
	if err := copyFile(
		filepath.Join(extractDir, "lib", "ld-musl-x86_64.so.1"),
		filepath.Join(newDir, "lib", "ld-musl-x86_64.so.1"),
	); err != nil {
		return fmt.Errorf("failed to copy musl: %w", err)
	}

	// Copy kernel modules from initramfs
	srcModules := filepath.Join(extractDir, "lib", "modules", kver)
	dstModules := filepath.Join(newDir, "lib", "modules", kver)
	if err := copyTree(srcModules, dstModules); err != nil {
		slog.Warn("Failed to copy some initramfs modules", "error", err)
	}

	// Extract modules from modloop
	slog.Debug("Extracting modules from modloop")
	modloopMount := filepath.Join(tmpDir, "modloop")
	if err := os.MkdirAll(modloopMount, 0755); err != nil {
		return err
	}

	// Extract specific modules we need
	modulePaths := []string{
		fmt.Sprintf("modules/%s/kernel/fs/ext4", kver),
		fmt.Sprintf("modules/%s/kernel/fs/jbd2", kver),
		fmt.Sprintf("modules/%s/kernel/fs/mbcache.ko", kver),
		fmt.Sprintf("modules/%s/kernel/lib", kver),
		fmt.Sprintf("modules/%s/kernel/drivers/net/virtio_net.ko", kver),
		// 9p filesystem modules
		fmt.Sprintf("modules/%s/kernel/net/9p", kver),
		fmt.Sprintf("modules/%s/kernel/fs/9p", kver),
		fmt.Sprintf("modules/%s/kernel/fs/netfs", kver),
	}

	for _, modPath := range modulePaths {
		cmd := exec.Command("unsquashfs", "-f", "-d", modloopMount, paths.ModloopPath, modPath)
		cmd.Run() // Ignore errors, some paths might not exist
	}

	// Copy extracted modules to new initramfs
	if err := copyTree(
		filepath.Join(modloopMount, "modules", kver, "kernel", "fs"),
		filepath.Join(newDir, "lib", "modules", kver, "kernel", "fs"),
	); err != nil {
		slog.Warn("Failed to copy fs modules", "error", err)
	}
	if err := copyTree(
		filepath.Join(modloopMount, "modules", kver, "kernel", "lib"),
		filepath.Join(newDir, "lib", "modules", kver, "kernel", "lib"),
	); err != nil {
		slog.Warn("Failed to copy lib modules", "error", err)
	}
	if err := copyTree(
		filepath.Join(modloopMount, "modules", kver, "kernel", "drivers", "net"),
		filepath.Join(newDir, "lib", "modules", kver, "kernel", "drivers", "net"),
	); err != nil {
		slog.Warn("Failed to copy net modules", "error", err)
	}
	// Copy 9p network modules
	if err := copyTree(
		filepath.Join(modloopMount, "modules", kver, "kernel", "net", "9p"),
		filepath.Join(newDir, "lib", "modules", kver, "kernel", "net", "9p"),
	); err != nil {
		slog.Warn("Failed to copy 9p net modules", "error", err)
	}

	// Run depmod
	cmd = exec.Command("depmod", "-b", newDir, kver)
	cmd.Run() // Ignore errors

	// Write our custom init script
	initPath := filepath.Join(newDir, "init")
	if err := os.WriteFile(initPath, []byte(initScriptContent), 0755); err != nil {
		return fmt.Errorf("failed to write init script: %w", err)
	}

	// Create cpio archive
	slog.Debug("Creating initramfs cpio archive")
	tmpInitramfs := paths.InitramfsPath + ".tmp"
	outFile, err := os.Create(tmpInitramfs)
	if err != nil {
		return err
	}
	defer outFile.Close()

	gzWriter := gzip.NewWriter(outFile)
	defer gzWriter.Close()

	cmd = exec.Command("sh", "-c", "find . | cpio -o -H newc 2>/dev/null")
	cmd.Dir = newDir
	cmd.Stdout = gzWriter
	if err := cmd.Run(); err != nil {
		os.Remove(tmpInitramfs)
		return fmt.Errorf("failed to create cpio archive: %w", err)
	}

	gzWriter.Close()
	outFile.Close()

	// Atomic rename
	if err := os.Rename(tmpInitramfs, paths.InitramfsPath); err != nil {
		os.Remove(tmpInitramfs)
		return err
	}

	slog.Info("Custom initramfs created", "path", paths.InitramfsPath)
	return nil
}

// buildRootfs creates an ext4 rootfs image using Docker and fuse2fs
func buildRootfs(paths *ImagePaths, sshPubKeyPath string) error {
	if _, err := os.Stat(paths.RootfsPath); err == nil {
		slog.Debug("Rootfs already exists", "path", paths.RootfsPath)
		return nil
	}

	slog.Info("Building rootfs image")

	// Read SSH public key
	sshPubKey, err := os.ReadFile(sshPubKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read SSH public key: %w", err)
	}

	// Create temp directory for build
	tmpDir, err := os.MkdirTemp("", "homura-rootfs-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Write Dockerfile
	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		return err
	}

	// Copy 9pvm-request source for Docker build from cache directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	ninepRequestSrc := filepath.Join(homeDir, ".cache", "homura", "src", "9pvm-request")
	if _, err := os.Stat(ninepRequestSrc); os.IsNotExist(err) {
		return fmt.Errorf("9pvm-request source not found at %s (run 'make 9p' to install)", ninepRequestSrc)
	}
	ninepRequestDst := filepath.Join(tmpDir, "9pvm-request")
	if err := copyTree(ninepRequestSrc, ninepRequestDst); err != nil {
		return fmt.Errorf("failed to copy 9pvm-request source: %w", err)
	}

	// Detect docker or podman
	dockerCmd := "docker"
	if _, err := exec.LookPath("podman"); err == nil {
		dockerCmd = "podman"
	}

	// Build Docker base image
	slog.Info("Building Docker base image (this may take a few minutes)")
	baseImageName := fmt.Sprintf("homura-vm-alpine-base:v%d", VMImplementationVersion)
	cmd := exec.Command(dockerCmd, "build",
		"--build-arg", fmt.Sprintf("SSH_PUB_KEY=%s", strings.TrimSpace(string(sshPubKey))),
		"-t", baseImageName,
		tmpDir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	// Determine final image to export
	finalImageName := baseImageName

	// Check for custom Dockerfile
	customDockerfilePath := filepath.Join(os.Getenv("HOME"), ".config", "homura", "Dockerfile.custom")
	if fileInfo, err := os.Stat(customDockerfilePath); err == nil && !fileInfo.IsDir() {
		slog.Info("Found custom Dockerfile", "path", customDockerfilePath)

		// Read custom Dockerfile contents
		customContent, err := os.ReadFile(customDockerfilePath)
		if err != nil {
			return fmt.Errorf("read custom Dockerfile: %w", err)
		}

		// Validate FROM line matches current version
		expectedFrom := fmt.Sprintf("FROM homura-vm-alpine-base:v%d", VMImplementationVersion)
		if err := validateCustomDockerfileVersion(string(customContent), expectedFrom); err != nil {
			return fmt.Errorf("invalid custom Dockerfile: %w\n\nPlease update %s:\n  Change the FROM line to: %s",
				err, customDockerfilePath, expectedFrom)
		}

		// Calculate MD5 hash of file contents for cache key
		hash := md5.Sum(customContent)
		hashPrefix := fmt.Sprintf("%x", hash)[:6]
		customImageName := fmt.Sprintf("homura-vm-alpine-custom:%s", hashPrefix)

		// Check if custom image already exists
		checkCmd := exec.Command(dockerCmd, "image", "inspect", customImageName)
		if err := checkCmd.Run(); err != nil {
			// Image doesn't exist, build it
			slog.Info("Building custom image", "tag", customImageName, "hash", hashPrefix)

			// Create temporary build directory
			tmpCustomDir := filepath.Join(os.TempDir(), fmt.Sprintf("homura-custom-build-%s", hashPrefix))
			if err := os.MkdirAll(tmpCustomDir, 0755); err != nil {
				return fmt.Errorf("create temp custom build dir: %w", err)
			}
			defer os.RemoveAll(tmpCustomDir)

			// Write user's Dockerfile as-is (no modification)
			customDockerfile := filepath.Join(tmpCustomDir, "Dockerfile")
			if err := os.WriteFile(customDockerfile, customContent, 0644); err != nil {
				return fmt.Errorf("write custom Dockerfile: %w", err)
			}

			// Build custom image
			buildCmd := exec.Command(dockerCmd, "build",
				"-t", customImageName,
				"-f", customDockerfile,
				tmpCustomDir,
			)
			buildCmd.Stdout = os.Stderr
			buildCmd.Stderr = os.Stderr

			if err := buildCmd.Run(); err != nil {
				return fmt.Errorf("build custom image: %w", err)
			}

			slog.Info("Custom image built successfully", "tag", customImageName)
		} else {
			slog.Info("Using cached custom image", "tag", customImageName)
		}

		finalImageName = customImageName
	}

	// Export container to tar
	tarPath := filepath.Join(tmpDir, "rootfs.tar")
	slog.Info("Exporting container to tar")

	containerName := fmt.Sprintf("homura-temp-%x", sha256.Sum256([]byte(tarPath)))
	cmd = exec.Command(dockerCmd, "create", "--name", containerName, finalImageName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker create failed: %w", err)
	}
	defer exec.Command(dockerCmd, "rm", containerName).Run()

	tarFile, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer tarFile.Close()

	cmd = exec.Command(dockerCmd, "export", containerName)
	cmd.Stdout = tarFile
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker export failed: %w", err)
	}
	tarFile.Close()

	// Create ext4 image
	slog.Info("Creating ext4 image")
	tmpRootfs := paths.RootfsPath + ".tmp"

	cmd = exec.Command("truncate", "-s", "1G", tmpRootfs)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("truncate failed: %w", err)
	}

	cmd = exec.Command("mke2fs", "-q", "-t", "ext4", "-O", "^metadata_csum,^64bit", "-L", "rootfs", tmpRootfs)
	if err := cmd.Run(); err != nil {
		os.Remove(tmpRootfs)
		return fmt.Errorf("mke2fs failed: %w", err)
	}

	// Mount with fuse2fs and extract tar
	slog.Info("Mounting image and extracting rootfs")
	mountDir, err := os.MkdirTemp("", "homura-mount-*")
	if err != nil {
		os.Remove(tmpRootfs)
		return err
	}
	defer os.RemoveAll(mountDir)

	// Mount
	cmd = exec.Command("fuse2fs", "-o", "fakeroot,rw", tmpRootfs, mountDir)
	if err := cmd.Run(); err != nil {
		os.Remove(tmpRootfs)
		return fmt.Errorf("fuse2fs mount failed: %w", err)
	}
	defer exec.Command("fusermount", "-u", mountDir).Run()

	// Extract tar
	tarFile2, err := os.Open(tarPath)
	if err != nil {
		os.Remove(tmpRootfs)
		return err
	}
	defer tarFile2.Close()

	cmd = exec.Command("tar", "--same-owner", "-xf", "-", "-C", mountDir)
	cmd.Stdin = tarFile2
	if err := cmd.Run(); err != nil {
		os.Remove(tmpRootfs)
		return fmt.Errorf("tar extract failed: %w", err)
	}

	// Add DNS config
	resolvPath := filepath.Join(mountDir, "etc", "resolv.conf")
	if err := os.WriteFile(resolvPath, []byte("nameserver 1.1.1.1\nnameserver 8.8.8.8\n"), 0644); err != nil {
		slog.Warn("Failed to write resolv.conf", "error", err)
	}

	// Add hosts file
	hostsPath := filepath.Join(mountDir, "etc", "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1\tlocalhost homura-vm\n::1\t\tlocalhost homura-vm\n"), 0644); err != nil {
		slog.Warn("Failed to write hosts", "error", err)
	}

	// Create 9p auto-mount script
	ninepScript := `#!/bin/sh
# Auto-mount 9p filesystem if kernel params present
if grep -q "p9.token=" /proc/cmdline; then
    mkdir -p /mnt/host
    mount -t 9p -o trans=tcp,port=5640 10.0.2.2 /mnt/host 2>/dev/null
    if [ $? -eq 0 ]; then
        echo "9p filesystem mounted at /mnt/host"
    else
        echo "Failed to mount 9p filesystem"
    fi
fi
`
	localDDir := filepath.Join(mountDir, "etc", "local.d")
	if err := os.MkdirAll(localDDir, 0755); err != nil {
		return fmt.Errorf("failed to create /etc/local.d: %w", err)
	}
	ninepScriptPath := filepath.Join(localDDir, "9pmount.start")
	if err := os.WriteFile(ninepScriptPath, []byte(ninepScript), 0755); err != nil {
		return fmt.Errorf("failed to write 9p mount script: %w", err)
	}

	// Copy SSH host keys
	if err := copyTree(paths.SSHHostKeysDir, filepath.Join(mountDir, "etc", "ssh")); err != nil {
		slog.Warn("Failed to copy SSH host keys", "error", err)
	}

	// Sync and unmount
	exec.Command("sync").Run()
	cmd = exec.Command("fusermount", "-u", mountDir)
	if err := cmd.Run(); err != nil {
		slog.Warn("Failed to unmount", "error", err)
	}

	// Atomic rename
	if err := os.Rename(tmpRootfs, paths.RootfsPath); err != nil {
		os.Remove(tmpRootfs)
		return err
	}

	slog.Info("Rootfs image created", "path", paths.RootfsPath)
	return nil
}

// CreateEphemeralDisk creates a sparse copy of the rootfs and resizes it
func CreateEphemeralDisk(basePath, destPath string, size string) error {
	slog.Info("Creating ephemeral disk", "base", basePath, "dest", destPath, "size", size)

	// Sparse copy
	cmd := exec.Command("cp", "--sparse=always", basePath, destPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sparse copy failed: %w", err)
	}

	// Resize
	cmd = exec.Command("truncate", "-s", size, destPath)
	if err := cmd.Run(); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("truncate failed: %w", err)
	}

	cmd = exec.Command("resize2fs", destPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("resize2fs failed: %w", err)
	}

	return nil
}

// copyFile copies a single file
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	// Copy permissions
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}

// copyTree recursively copies a directory tree
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return copyFile(path, dstPath)
	})
}

// validateCustomDockerfileVersion checks that the FROM line matches expected base image version
func validateCustomDockerfileVersion(content, expectedFrom string) error {
	// Parse FROM line (handles comments and whitespace)
	lines := strings.Split(content, "\n")
	var fromLine string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "FROM ") {
			fromLine = trimmed
			break
		}
	}

	if fromLine == "" {
		return fmt.Errorf("no FROM line found")
	}

	// Extract image name (before any AS alias)
	parts := strings.Fields(fromLine)
	if len(parts) < 2 {
		return fmt.Errorf("invalid FROM line: %s", fromLine)
	}

	fromImage := parts[1]
	expectedImage := strings.TrimPrefix(expectedFrom, "FROM ")

	if fromImage != expectedImage {
		return fmt.Errorf("base image version mismatch: found '%s', expected '%s'",
			fromImage, expectedImage)
	}

	return nil
}
