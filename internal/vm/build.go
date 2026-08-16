package vm

import (
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	alpineVersion = "3.21"
	alpineRelease = "3.21.3"
	alpineMirror  = "https://dl-cdn.alpinelinux.org"
	rootfsDistro  = "ubuntu-24.04"
)

//go:embed resources/Dockerfile
var dockerfileContent string

//go:embed resources/init
var initScriptContent string

//go:embed resources/CLAUDE.md
var builtinClaudeMd string

// ImagePaths holds paths to all VM images
type ImagePaths struct {
	CacheDir        string
	KernelPath      string
	InitramfsPath   string
	ModloopPath     string
	RootfsPath      string
	SSHHostKeysDir  string
	EphemeralRootfs string
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

	// Rootfs filename - will be updated later if custom Dockerfile exists
	rootfsFilename := "rootfs.ext4"

	paths := &ImagePaths{
		CacheDir:       cacheDir,
		KernelPath:     filepath.Join(cacheDir, "vmlinuz-virt"),
		InitramfsPath:  filepath.Join(cacheDir, "initramfs-virt"),
		ModloopPath:    filepath.Join(cacheDir, "modloop-virt"),
		RootfsPath:     filepath.Join(cacheDir, rootfsFilename),
		SSHHostKeysDir: sshKeysDir,
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
	root, err := CacheDir()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(root, fmt.Sprintf("v%d", VMImplementationVersion), "vm-images", rootfsDistro)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	return cacheDir, nil
}

// getSSHKeysDir returns the stable SSH host keys directory (not versioned)
func getSSHKeysDir() (string, error) {
	root, err := CacheDir()
	if err != nil {
		return "", err
	}
	sshKeysDir := filepath.Join(root, "ssh_host_keys")
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
		// virtio drivers
		fmt.Sprintf("modules/%s/kernel/drivers/virtio", kver),
		fmt.Sprintf("modules/%s/kernel/drivers/block/virtio_blk.ko", kver),
		fmt.Sprintf("modules/%s/kernel/drivers/net/virtio_net.ko", kver),
		// FUSE for 9pfuse driver (kernel 9p not used - homura uses FUSE-based 9pfuse)
		fmt.Sprintf("modules/%s/kernel/fs/fuse", kver),
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

// detectContainerCmd returns the container CLI to use ("docker" or "podman").
// Honors HOMURA_CONTAINER_CMD if set. Otherwise prefers docker, falls back to podman.
func detectContainerCmd() (string, error) {
	if override := os.Getenv("HOMURA_CONTAINER_CMD"); override != "" {
		if _, err := exec.LookPath(override); err != nil {
			return "", fmt.Errorf("HOMURA_CONTAINER_CMD=%q not found in PATH: %w", override, err)
		}
		return override, nil
	}
	for _, candidate := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("neither docker nor podman found in PATH; install one or set HOMURA_CONTAINER_CMD")
}

// cleanupStaleContainers removes any leftover homura containers from previous failed builds
func cleanupStaleContainers(dockerCmd string) {
	// List all containers (running and stopped) with names starting with "homura-temp-"
	cmd := exec.Command(dockerCmd, "ps", "-a", "--filter", "name=homura-temp-", "--format", "{{.Names}}")
	output, err := cmd.Output()
	if err != nil {
		return // Ignore errors, this is best-effort cleanup
	}

	containers := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, container := range containers {
		container = strings.TrimSpace(container)
		if container == "" {
			continue
		}
		slog.Info("Cleaning up stale container from previous build", "container", container)
		// Force remove in case it's still running
		rmCmd := exec.Command(dockerCmd, "rm", "-f", container)
		rmCmd.Run() // Ignore errors
	}
}

// buildRootfs creates an ext4 rootfs image using Docker or Podman and mke2fs -d
func buildRootfs(paths *ImagePaths, sshPubKeyPath string) error {
	slog.Info("Building rootfs image")

	// Check for fakeroot (required for correct ownership in ext4 image)
	if _, err := exec.LookPath("fakeroot"); err != nil {
		return fmt.Errorf("fakeroot required but not found: install via your package manager")
	}

	// Read SSH public key
	sshPubKey, err := os.ReadFile(sshPubKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read SSH public key: %w", err)
	}

	dockerCmd, err := detectContainerCmd()
	if err != nil {
		return err
	}
	slog.Info("Using container engine", "cmd", dockerCmd)

	// Clean up any stale containers from previous failed builds
	cleanupStaleContainers(dockerCmd)

	// Check if base image already exists
	baseImageName := fmt.Sprintf("homura-vm-ubuntu-base:v%d", VMImplementationVersion)
	baseImageExists := false
	checkBaseCmd := exec.Command(dockerCmd, "image", "inspect", baseImageName)
	if err := checkBaseCmd.Run(); err == nil {
		baseImageExists = true
	}

	// Get base image ID (or placeholder if image doesn't exist yet)
	var baseImageID string
	if baseImageExists {
		baseImageID, err = getImageID(dockerCmd, baseImageName)
		if err != nil {
			return fmt.Errorf("failed to get docker image id: %w", err)
		}
	}

	// Determine final rootfs filename and image name BEFORE checking if rootfs exists
	finalImageName := baseImageName
	customDockerfilePath := filepath.Join(os.Getenv("HOME"), ".config", "homura", "Dockerfile.custom")
	var customContent []byte
	hasCustomDockerfile := false

	if fileInfo, err := os.Stat(customDockerfilePath); err == nil && !fileInfo.IsDir() {
		if content, err := os.ReadFile(customDockerfilePath); err == nil {
			customContent = content
			hasCustomDockerfile = true

			// If base image exists, calculate the hash for rootfs filename
			if baseImageID != "" {
				combinedHash := hashCustomImage(customContent, baseImageID)
				newRootfsFilename := fmt.Sprintf("rootfs-%s.ext4", combinedHash)
				paths.RootfsPath = filepath.Join(paths.CacheDir, newRootfsFilename)
				slog.Debug("Using custom rootfs filename", "filename", newRootfsFilename, "hash", combinedHash)
			}
		}
	}

	// Now check if rootfs already exists (with the correct filename)
	if _, err := os.Stat(paths.RootfsPath); err == nil {
		slog.Debug("Rootfs already exists", "path", paths.RootfsPath)
		return nil
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
	root, err := CacheDir()
	if err != nil {
		return fmt.Errorf("failed to get cache directory: %w", err)
	}
	ninepRequestSrc := filepath.Join(root, "src", "9pvm-request")
	if _, err := os.Stat(ninepRequestSrc); os.IsNotExist(err) {
		return fmt.Errorf("9pvm-request source not found at %s (run 'make 9p' to install)", ninepRequestSrc)
	}
	ninepRequestDst := filepath.Join(tmpDir, "9pvm-request")
	if err := copyTree(ninepRequestSrc, ninepRequestDst); err != nil {
		return fmt.Errorf("failed to copy 9pvm-request source: %w", err)
	}

	// Copy 9pfuse source for Docker build from cache directory
	ninepfuseSrc := filepath.Join(root, "src", "9pfuse")
	if _, err := os.Stat(ninepfuseSrc); os.IsNotExist(err) {
		return fmt.Errorf("9pfuse source not found at %s (run 'make 9p' to install)", ninepfuseSrc)
	}
	ninepfuseDst := filepath.Join(tmpDir, "9pfuse")
	if err := copyTree(ninepfuseSrc, ninepfuseDst); err != nil {
		return fmt.Errorf("failed to copy 9pfuse source: %w", err)
	}

	// Copy test-fs source for Docker build from cache directory
	testfsSrc := filepath.Join(root, "src", "test-fs")
	if _, err := os.Stat(testfsSrc); os.IsNotExist(err) {
		return fmt.Errorf("test-fs source not found at %s (run 'make 9p' to install)", testfsSrc)
	}
	testfsDst := filepath.Join(tmpDir, "test-fs")
	if err := copyTree(testfsSrc, testfsDst); err != nil {
		return fmt.Errorf("failed to copy test-fs source: %w", err)
	}

	// Build base container image if needed
	if !baseImageExists {
		slog.Info("Building container base image (this may take a few minutes)")
		cmd := exec.Command(dockerCmd, "build",
			"--build-arg", fmt.Sprintf("SSH_PUB_KEY=%s", strings.TrimSpace(string(sshPubKey))),
			"-t", baseImageName,
			tmpDir)
		cmd.Stdout = ChildOutput
		cmd.Stderr = ChildOutput
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s build failed: %w", dockerCmd, err)
		}

		// Get the new base image ID
		baseImageID, err = getImageID(dockerCmd, baseImageName)
		if err != nil {
			return fmt.Errorf("failed to get base image ID: %w", err)
		}

		// Recalculate rootfs path with actual base image ID if custom Dockerfile exists
		if hasCustomDockerfile {
			combinedHash := hashCustomImage(customContent, baseImageID)
			newRootfsFilename := fmt.Sprintf("rootfs-%s.ext4", combinedHash)
			paths.RootfsPath = filepath.Join(paths.CacheDir, newRootfsFilename)
			slog.Debug("Using custom rootfs filename", "filename", newRootfsFilename, "hash", combinedHash)
		}
	} else {
		slog.Info("Using cached container base image")
	}

	// Handle custom Dockerfile
	if hasCustomDockerfile {
		slog.Info("Found custom Dockerfile", "path", customDockerfilePath)

		// Validate FROM line matches current version
		expectedFrom := fmt.Sprintf("FROM homura-vm-ubuntu-base:v%d", VMImplementationVersion)
		if err := validateCustomDockerfileVersion(string(customContent), expectedFrom); err != nil {
			return fmt.Errorf("invalid custom Dockerfile: %w\n\nPlease update %s:\n  Change the FROM line to: %s",
				err, customDockerfilePath, expectedFrom)
		}

		// Calculate hash combining Dockerfile content + base image ID
		// This ensures rebuild when either the custom Dockerfile OR base image changes
		combinedHash := hashCustomImage(customContent, baseImageID)
		customImageName := fmt.Sprintf("homura-vm-ubuntu-custom:%s", combinedHash)

		// Check if custom image already exists
		checkCmd := exec.Command(dockerCmd, "image", "inspect", customImageName)
		if err := checkCmd.Run(); err != nil {
			// Image doesn't exist, build it
			slog.Info("Building custom image", "tag", customImageName, "hash", combinedHash)

			// Create temporary build directory
			tmpCustomDir := filepath.Join(os.TempDir(), fmt.Sprintf("homura-custom-build-%s", combinedHash))
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
			buildCmd.Stdout = ChildOutput
			buildCmd.Stderr = ChildOutput

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
	cmd := exec.Command(dockerCmd, "create", "--name", containerName, finalImageName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s create failed: %w", dockerCmd, err)
	}
	defer exec.Command(dockerCmd, "rm", "-f", containerName).Run() // Force remove to handle stuck containers

	tarFile, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer tarFile.Close()

	cmd = exec.Command(dockerCmd, "export", containerName)
	cmd.Stdout = tarFile
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s export failed: %w", dockerCmd, err)
	}
	tarFile.Close()

	// Extract kernel modules from modloop (this runs as current user, before fakeroot)
	slog.Info("Extracting kernel modules from modloop")
	tmpModuleDir, err := os.MkdirTemp("", "homura-modules-*")
	if err != nil {
		return fmt.Errorf("failed to create temp module dir: %w", err)
	}
	defer os.RemoveAll(tmpModuleDir)

	// Detect kernel version from modloop
	cmd = exec.Command("unsquashfs", "-ll", paths.ModloopPath)
	output, err := cmd.Output()
	var kver string
	if err == nil {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "modules/") && strings.Contains(line, "-virt/") {
				parts := strings.Split(line, "modules/")
				if len(parts) > 1 {
					verParts := strings.Split(parts[1], "/")
					if len(verParts) > 0 {
						kver = verParts[0]
						break
					}
				}
			}
		}
	}

	if kver != "" {
		modulePaths := []string{
			fmt.Sprintf("modules/%s/kernel/fs/fuse", kver),
			fmt.Sprintf("modules/%s/kernel/fs/overlayfs", kver),
			fmt.Sprintf("modules/%s/kernel/net/netfilter", kver),
			fmt.Sprintf("modules/%s/kernel/net/ipv4/netfilter", kver),
			fmt.Sprintf("modules/%s/kernel/net/ipv6/netfilter", kver),
			fmt.Sprintf("modules/%s/kernel/net/bridge", kver),
			fmt.Sprintf("modules/%s/kernel/net/802", kver),
			fmt.Sprintf("modules/%s/kernel/net/llc", kver),
			fmt.Sprintf("modules/%s/kernel/drivers/net/veth.ko", kver),
			fmt.Sprintf("modules/%s/kernel/drivers/net/tun.ko", kver),
			fmt.Sprintf("modules/%s/kernel/drivers/block/loop.ko", kver),
			fmt.Sprintf("modules/%s/kernel/drivers/net/dummy.ko", kver),
			fmt.Sprintf("modules/%s/kernel/drivers/net/wireguard", kver),
			fmt.Sprintf("modules/%s/kernel/lib", kver),
			fmt.Sprintf("modules/%s/kernel/crypto", kver),
			fmt.Sprintf("modules/%s/kernel/arch/x86/crypto", kver),
		}

		for _, modPath := range modulePaths {
			cmd := exec.Command("unsquashfs", "-f", "-d", tmpModuleDir, paths.ModloopPath, modPath)
			cmd.Run() // Ignore errors, some paths might not exist
		}
		slog.Info("Kernel modules extracted", "version", kver)
	}

	// Write config files to inject directory (native filesystem, fast)
	injectDir := filepath.Join(tmpDir, "inject")
	if err := os.MkdirAll(injectDir, 0755); err != nil {
		return fmt.Errorf("failed to create inject dir: %w", err)
	}

	// resolv.conf
	if err := os.WriteFile(filepath.Join(injectDir, "resolv.conf"), []byte("nameserver 1.1.1.1\nnameserver 8.8.8.8\n"), 0644); err != nil {
		return fmt.Errorf("failed to write resolv.conf: %w", err)
	}

	// hosts
	if err := os.WriteFile(filepath.Join(injectDir, "hosts"), []byte("127.0.0.1\tlocalhost homura-vm\n::1\t\tlocalhost homura-vm\n"), 0644); err != nil {
		return fmt.Errorf("failed to write hosts: %w", err)
	}

	// hostname
	if err := os.WriteFile(filepath.Join(injectDir, "hostname"), []byte("homura-vm\n"), 0644); err != nil {
		return fmt.Errorf("failed to write hostname: %w", err)
	}

	// fsmount.sh (filesystem mount script)
	ninepScript := `#!/bin/sh
# Auto-mount filesystem (virtiofs or 9p via FUSE) based on kernel params

# Log all output to file
exec >> /var/log/fsmount.log 2>&1
echo "=== fsmount.sh running at $(date) ==="

# Ensure hostname is set (backup in case hostname service didn't run)
hostname -F /etc/hostname 2>/dev/null || hostname homura-vm

mkdir -p /mnt/host

# Check for virtiofs mode first
if grep -q "virtiofs.token=" /proc/cmdline; then
    echo "Using virtiofs mode"

    # Mount virtiofs - the kernel driver handles this directly
    mount -t virtiofs hostfs /mnt/host

    if mountpoint -q /mnt/host; then
        echo "virtiofs mounted at /mnt/host"
    else
        echo "Failed to mount virtiofs"
    fi

# Fall back to 9p/FUSE mode
elif grep -q "p9.token=" /proc/cmdline; then
    echo "Using 9p/FUSE mode"

    # Extract 9p listen port from kernel cmdline
    P9_PORT=$(grep -o 'p9.listenport=[0-9]*' /proc/cmdline | cut -d= -f2)
    if [ -z "$P9_PORT" ]; then
        echo "ERROR: p9.listenport not found in kernel cmdline"
        exit 1
    fi

    # Load FUSE module
    modprobe fuse 2>/dev/null

    # Start 9pfuse in background
    /usr/local/bin/9pfuse -server 10.0.2.2:$P9_PORT -mount /mnt/host &

    # Poll for the mount instead of a fixed sleep (this service runs
    # Before=ssh.service, so every wasted moment here delays SSH)
    TRIES=0
    while ! mountpoint -q /mnt/host && [ $TRIES -lt 100 ]; do
        sleep 0.05
        TRIES=$((TRIES + 1))
    done

    if mountpoint -q /mnt/host; then
        echo "9p filesystem mounted at /mnt/host (FUSE) on port $P9_PORT"
    else
        echo "Failed to mount 9p filesystem via FUSE"
    fi
else
    echo "No filesystem sharing mode detected in /proc/cmdline"
fi

# Common post-mount setup: symlink Claude config files
if mountpoint -q /mnt/host; then
    HOST_HOME=$(grep -o 'host.home=[^ ]*' /proc/cmdline | cut -d= -f2)
    echo "HOST_HOME='$HOST_HOME'"
    if [ -n "$HOST_HOME" ]; then
        # Wait for filesystem to be ready to serve paths (retry up to 5 times)
        CLAUDE_DIR="/mnt/host${HOST_HOME}/.claude"
        CLAUDE_JSON="/mnt/host${HOST_HOME}/.claude.json"
        echo "Looking for CLAUDE_DIR='$CLAUDE_DIR' CLAUDE_JSON='$CLAUDE_JSON'"
        RETRY=0
        while [ $RETRY -lt 25 ]; do
            # Check if at least one claude config exists, break if found
            if [ -d "$CLAUDE_DIR" ] || [ -f "$CLAUDE_JSON" ]; then
                echo "Found claude config on attempt $RETRY"
                break
            fi
            RETRY=$((RETRY + 1))
            echo "Waiting for filesystem to serve claude config (attempt $RETRY/25)..."
            sleep 0.2
        done

        if [ $RETRY -eq 25 ]; then
            echo "WARNING: Timed out waiting for claude config"
            echo "Contents of /mnt/host${HOST_HOME}/:"
            ls -la "/mnt/host${HOST_HOME}/" 2>&1 || echo "(ls failed)"
        fi

        # Symlink .claude.json
        if [ -f "$CLAUDE_JSON" ]; then
            ln -sf "$CLAUDE_JSON" /root/.claude.json
            echo "Symlinked /root/.claude.json"
        fi
        # Symlink .claude directory
        if [ -d "$CLAUDE_DIR" ]; then
            ln -sf "$CLAUDE_DIR" /root/.claude
            echo "Symlinked /root/.claude"
        fi
        # Symlink .claude.lock
        CLAUDE_LOCK="/mnt/host${HOST_HOME}/.claude.lock"
        ln -sf "$CLAUDE_LOCK" /root/.claude.lock
        echo "Symlinked /root/.claude.lock"

        # Append user VM CLAUDE.md customizations if they exist
        USER_CLAUDE="/mnt/host${HOST_HOME}/.config/homura/CLAUDE.md"
        if [ -f "$USER_CLAUDE" ]; then
            echo "" >> /etc/homura/claude-config/CLAUDE.md
            echo "# User Customizations" >> /etc/homura/claude-config/CLAUDE.md
            echo "" >> /etc/homura/claude-config/CLAUDE.md
            cat "$USER_CLAUDE" >> /etc/homura/claude-config/CLAUDE.md
            echo "Appended user CLAUDE.md customizations"
        fi

        # Inject VM networking info into CLAUDE.md
        VM_HOSTIP=$(grep -o 'vm.hostip=[^ ]*' /proc/cmdline | cut -d= -f2)
        VM_SSHPORT=$(grep -o 'vm.sshport=[^ ]*' /proc/cmdline | cut -d= -f2)
        VM_PORTSTART=$(grep -o 'vm.portstart=[^ ]*' /proc/cmdline | cut -d= -f2)
        VM_PORTEND=$(grep -o 'vm.portend=[^ ]*' /proc/cmdline | cut -d= -f2)
        VM_SLOT=$(grep -o 'vm.slot=[^ ]*' /proc/cmdline | cut -d= -f2)
        if [ -n "$VM_HOSTIP" ]; then
            FIRST_FWD_PORT=$(($VM_SSHPORT + 1))
            cat >> /etc/homura/claude-config/CLAUDE.md <<VMEOF

## VM Networking

This VM is slot $VM_SLOT on the host.

- **Host-side IP:** $VM_HOSTIP
- **SSH from host:** ssh -p $VM_SSHPORT root@$VM_HOSTIP
- **Port range:** $VM_PORTSTART-$VM_PORTEND (first port is SSH, remaining 9 are 1:1 passthrough)
- **Guest IP:** 10.0.2.15 (gateway to host: 10.0.2.2)

To expose a service running in this VM to the host, bind it to 0.0.0.0 on one of the passthrough ports ($FIRST_FWD_PORT-$VM_PORTEND inside the VM maps to $VM_HOSTIP:$FIRST_FWD_PORT-$VM_PORTEND on the host).
VMEOF
            echo "Injected VM networking info into CLAUDE.md"
        fi

        # Symlink host home path so host absolute paths work in VM
        if [ -n "$HOST_HOME" ] && [ "$HOST_HOME" != "/root" ]; then
            mkdir -p "$(dirname "$HOST_HOME")"
            ln -sfn "/mnt/host${HOST_HOME}" "$HOST_HOME"
            echo "Symlinked $HOST_HOME -> /mnt/host${HOST_HOME}"
        fi
    fi
fi

echo "=== fsmount.sh finished at $(date) ==="
`
	if err := os.WriteFile(filepath.Join(injectDir, "fsmount.sh"), []byte(ninepScript), 0755); err != nil {
		return fmt.Errorf("failed to write fsmount.sh: %w", err)
	}

	fsmountService := `[Unit]
Description=Homura host filesystem mount
After=local-fs.target
Before=ssh.service

[Service]
Type=oneshot
ExecStart=/usr/local/lib/homura/fsmount.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
	if err := os.WriteFile(filepath.Join(injectDir, "homura-fsmount.service"), []byte(fsmountService), 0644); err != nil {
		return fmt.Errorf("failed to write homura-fsmount.service: %w", err)
	}

	// swap.sh
	swapScript := `#!/bin/sh
# Create and enable swap file on boot

# Log all output to file
exec >> /var/log/swap.log 2>&1
echo "=== swap.sh running at $(date) ==="

SWAPFILE=/var/swap
SWAPSIZE=1G

# Only create if it doesn't exist
if [ ! -f "$SWAPFILE" ]; then
    echo "Creating ${SWAPSIZE} swap file..."
    # fallocate is near-instant on ext4 (no holes, safe for swap); the disk is
    # ephemeral so this runs on every boot - writing 1GiB of zeros with dd
    # would compete with all other boot I/O. dd stays as a fallback.
    fallocate -l "$SWAPSIZE" "$SWAPFILE" || dd if=/dev/zero of="$SWAPFILE" bs=1M count=1024
    chmod 600 "$SWAPFILE"
    mkswap "$SWAPFILE"
fi

# Enable swap
swapon "$SWAPFILE" 2>/dev/null && echo "Swap enabled: $SWAPFILE"

echo "=== swap.sh finished at $(date) ==="
`
	if err := os.WriteFile(filepath.Join(injectDir, "swap.sh"), []byte(swapScript), 0755); err != nil {
		return fmt.Errorf("failed to write swap.sh: %w", err)
	}

	swapService := `[Unit]
Description=Homura swapfile setup
After=local-fs.target

[Service]
Type=oneshot
ExecStart=/usr/local/lib/homura/swap.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
	if err := os.WriteFile(filepath.Join(injectDir, "homura-swap.service"), []byte(swapService), 0644); err != nil {
		return fmt.Errorf("failed to write homura-swap.service: %w", err)
	}

	// docker-modules.sh
	dockerModulesScript := `#!/bin/sh
# Load kernel modules required for Docker/container support
# This script runs early in boot to ensure modules are available before Docker starts
# Module load order matters due to dependencies!

exec >> /var/log/docker-modules.log 2>&1
echo "=== docker-modules.sh running at $(date) ==="

# Overlay filesystem (Docker's preferred storage driver)
modprobe overlay && echo "Loaded: overlay"

# Crypto modules (required by nf_conntrack)
modprobe crc32c_generic && echo "Loaded: crc32c_generic"
modprobe crc32c_intel 2>/dev/null && echo "Loaded: crc32c_intel (hardware)"
modprobe libcrc32c && echo "Loaded: libcrc32c"

# LLC and STP protocols (required by bridge)
modprobe llc && echo "Loaded: llc"
modprobe stp && echo "Loaded: stp"

# Bridge networking (Docker bridge network)
modprobe bridge && echo "Loaded: bridge"
modprobe br_netfilter && echo "Loaded: br_netfilter"

# Virtual ethernet pairs (container networking)
modprobe veth && echo "Loaded: veth"

# Netfilter/iptables modules (Docker networking/NAT)
modprobe nf_conntrack && echo "Loaded: nf_conntrack"
modprobe nf_nat && echo "Loaded: nf_nat"
modprobe nf_defrag_ipv4 && echo "Loaded: nf_defrag_ipv4"
modprobe x_tables && echo "Loaded: x_tables"
modprobe ip_tables && echo "Loaded: ip_tables"
modprobe iptable_nat && echo "Loaded: iptable_nat"
modprobe iptable_filter && echo "Loaded: iptable_filter"
modprobe xt_MASQUERADE && echo "Loaded: xt_MASQUERADE"
modprobe xt_addrtype && echo "Loaded: xt_addrtype"
modprobe xt_conntrack && echo "Loaded: xt_conntrack"

echo "=== docker-modules.sh finished at $(date) ==="
`
	if err := os.WriteFile(filepath.Join(injectDir, "docker-modules.sh"), []byte(dockerModulesScript), 0755); err != nil {
		return fmt.Errorf("failed to write docker-modules.sh: %w", err)
	}

	dockerModulesService := `[Unit]
Description=Homura Docker kernel modules
After=local-fs.target systemd-modules-load.service
Before=docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/lib/homura/docker-modules.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
	if err := os.WriteFile(filepath.Join(injectDir, "homura-docker-modules.service"), []byte(dockerModulesService), 0644); err != nil {
		return fmt.Errorf("failed to write homura-docker-modules.service: %w", err)
	}

	// CLAUDE.md
	claudeConfigInjectDir := filepath.Join(injectDir, "claude-config")
	if err := os.MkdirAll(claudeConfigInjectDir, 0755); err != nil {
		return fmt.Errorf("failed to create claude-config inject dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(claudeConfigInjectDir, "CLAUDE.md"), []byte(builtinClaudeMd), 0644); err != nil {
		return fmt.Errorf("failed to write CLAUDE.md: %w", err)
	}

	// Build the staging + image creation script to run under fakeroot
	slog.Info("Creating ext4 image with fakeroot + mke2fs -d")
	stagingDir := filepath.Join(tmpDir, "staging")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return fmt.Errorf("failed to create staging dir: %w", err)
	}

	tmpRootfs := paths.RootfsPath + ".tmp"

	// Build the fakeroot script
	// Use kver="" if we couldn't detect it to skip module copying
	fakerootScript := fmt.Sprintf(`set -e

# 1. Extract Docker tar into staging dir
tar --same-owner -xf "%s" -C "%s"

# 2. Remove Docker markers
rm -f "%s/.dockerenv"
rm -rf "%s/run/.containerenv"

# 3. Copy injected config files
cp "%s/resolv.conf" "%s/etc/resolv.conf"
cp "%s/hosts" "%s/etc/hosts"
cp "%s/hostname" "%s/etc/hostname"

mkdir -p "%s/usr/local/lib/homura"
cp "%s/fsmount.sh" "%s/usr/local/lib/homura/fsmount.sh"
chmod 755 "%s/usr/local/lib/homura/fsmount.sh"
cp "%s/swap.sh" "%s/usr/local/lib/homura/swap.sh"
chmod 755 "%s/usr/local/lib/homura/swap.sh"
cp "%s/docker-modules.sh" "%s/usr/local/lib/homura/docker-modules.sh"
chmod 755 "%s/usr/local/lib/homura/docker-modules.sh"

mkdir -p "%s/etc/systemd/system"
mkdir -p "%s/etc/systemd/system/multi-user.target.wants"
cp "%s/homura-fsmount.service" "%s/etc/systemd/system/homura-fsmount.service"
cp "%s/homura-swap.service" "%s/etc/systemd/system/homura-swap.service"
cp "%s/homura-docker-modules.service" "%s/etc/systemd/system/homura-docker-modules.service"
ln -sf /etc/systemd/system/homura-fsmount.service "%s/etc/systemd/system/multi-user.target.wants/homura-fsmount.service"
ln -sf /etc/systemd/system/homura-swap.service "%s/etc/systemd/system/multi-user.target.wants/homura-swap.service"
ln -sf /etc/systemd/system/homura-docker-modules.service "%s/etc/systemd/system/multi-user.target.wants/homura-docker-modules.service"

mkdir -p "%s/etc/homura/claude-config"
cp "%s/CLAUDE.md" "%s/etc/homura/claude-config/CLAUDE.md"
`,
		tarPath, stagingDir,
		stagingDir,
		stagingDir,
		injectDir, stagingDir,
		injectDir, stagingDir,
		injectDir, stagingDir,
		stagingDir,
		injectDir, stagingDir,
		stagingDir,
		injectDir, stagingDir,
		stagingDir,
		injectDir, stagingDir,
		stagingDir,
		stagingDir,
		stagingDir,
		injectDir, stagingDir,
		injectDir, stagingDir,
		injectDir, stagingDir,
		stagingDir,
		stagingDir,
		stagingDir,
		stagingDir,
		claudeConfigInjectDir, stagingDir,
	)

	// Add kernel module copying if we have modules
	if kver != "" {
		modulesSrc := filepath.Join(tmpModuleDir, "modules", kver, "kernel")
		modulesDst := fmt.Sprintf("%s/lib/modules/%s/kernel", stagingDir, kver)

		fakerootScript += fmt.Sprintf(`
# 4. Copy kernel modules
mkdir -p "%s"
`, modulesDst)

		for _, subdir := range []string{"fs", "net", "drivers", "lib", "crypto", "arch"} {
			src := filepath.Join(modulesSrc, subdir)
			dst := filepath.Join(modulesDst, subdir)
			// Only add copy if source exists
			if _, err := os.Stat(src); err == nil {
				fakerootScript += fmt.Sprintf(`cp -a "%s" "%s" 2>/dev/null || true
`, src, dst)
			}
		}

		fakerootScript += fmt.Sprintf(`
# 5. Run depmod
depmod -b "%s" "%s" || true
`, stagingDir, kver)
	}

	// Add SSH host key copying
	fakerootScript += fmt.Sprintf(`
# 6. Copy SSH host keys
mkdir -p "%s/etc/ssh"
cp -a "%s"/* "%s/etc/ssh/" 2>/dev/null || true

# 7. Size the ext4 image based on the staged rootfs with extra headroom.
ROOTFS_KB=$(du -sk "%s" | cut -f1)
ROOTFS_KB=$((ROOTFS_KB + ROOTFS_KB / 3 + 524288))
MIN_ROOTFS_KB=$((3072 * 1024))
if [ "$ROOTFS_KB" -lt "$MIN_ROOTFS_KB" ]; then
    ROOTFS_KB=$MIN_ROOTFS_KB
fi
truncate -s "${ROOTFS_KB}K" "%s"
mke2fs -q -t ext4 -O "^metadata_csum,^64bit" -E root_owner=0:0 -L rootfs -d "%s" "%s"

# 8. Grow the image to its final runtime size once, at build time, so that
# per-start disk prep is a metadata-only qcow2 overlay (no copy, no resize2fs).
# The file stays sparse, so cache disk usage barely changes.
truncate -s %s "%s"
resize2fs "%s"
`, stagingDir, paths.SSHHostKeysDir, stagingDir,
		stagingDir, tmpRootfs, stagingDir, tmpRootfs,
		EphemeralDiskSize, tmpRootfs, tmpRootfs,
	)

	// Write the script to a temp file and run it under fakeroot
	scriptPath := filepath.Join(tmpDir, "build-rootfs.sh")
	if err := os.WriteFile(scriptPath, []byte(fakerootScript), 0755); err != nil {
		return fmt.Errorf("failed to write fakeroot script: %w", err)
	}

	cmd = exec.Command("fakeroot", "sh", scriptPath)
	cmd.Stdout = ChildOutput
	cmd.Stderr = ChildOutput
	if err := cmd.Run(); err != nil {
		os.Remove(tmpRootfs)
		return fmt.Errorf("fakeroot rootfs build failed: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpRootfs, paths.RootfsPath); err != nil {
		os.Remove(tmpRootfs)
		return err
	}

	slog.Info("Rootfs image created", "path", paths.RootfsPath)
	return nil
}

// EphemeralDiskSize is the runtime size of the guest rootfs. The base image
// is grown (sparsely) to this size once at build time, so per-start disk prep
// never needs to copy or resize anything.
const EphemeralDiskSize = "50G"

// CreateEphemeralDisk creates a qcow2 overlay backed by the (read-only) base
// rootfs image. The base is already at its final runtime size, so this is a
// near-instant metadata-only operation; guest writes land in the overlay.
func CreateEphemeralDisk(basePath, destPath string) error {
	start := time.Now()

	cmd := exec.Command("qemu-img", "create", "-q", "-f", "qcow2", "-b", basePath, "-F", "raw", destPath)
	cmd.Stdout = ChildOutput
	cmd.Stderr = ChildOutput
	if err := cmd.Run(); err != nil {
		if _, lookErr := exec.LookPath("qemu-img"); lookErr != nil {
			return fmt.Errorf("qemu-img not found in PATH (usually packaged as qemu-utils / qemu-img): %w", err)
		}
		os.Remove(destPath)
		return fmt.Errorf("qemu-img create overlay failed: %w", err)
	}

	slog.Info("Ephemeral disk overlay created", "base", basePath, "dest", destPath, "took", time.Since(start))
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

// getImageID retrieves the image ID for a given image name/tag
func getImageID(dockerCmd, imageName string) (string, error) {
	cmd := exec.Command(dockerCmd, "image", "inspect", "--format={{.Id}}", imageName)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect image %s: %w", imageName, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// hashCustomImage creates a combined hash of the custom Dockerfile content and base image ID
// This ensures the custom image rebuilds when either the Dockerfile OR the base image changes
func hashCustomImage(dockerfileContent []byte, baseImageID string) string {
	// Hash the Dockerfile content
	contentHash := md5.Sum(dockerfileContent)
	contentHashStr := fmt.Sprintf("%x", contentHash)

	// Hash the base image ID
	baseImageHash := md5.Sum([]byte(baseImageID))
	baseImageHashStr := fmt.Sprintf("%x", baseImageHash)

	// Combine both hashes and hash again
	combined := contentHashStr + baseImageHashStr
	finalHash := md5.Sum([]byte(combined))

	// Return first 6 characters for brevity
	return fmt.Sprintf("%x", finalHash)[:6]
}
