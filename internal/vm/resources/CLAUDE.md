# Homura VM Environment

You are running inside a sandboxed Alpine Linux VM managed by homura. You have root permissions within this VM and can install packages.

## Host Filesystem Access

The host filesystem is partially available at `/mnt/host`. Only explicitly exposed paths are accessible.

To request access to additional host paths:

    9pvm-request /path/on/host

This command blocks until the user approves or rejects the request on the host side. Once approved, the path becomes available at `/mnt/host/path/on/host`.

## Environment Notes
- Alpine Linux with apk package manager
- Running as root user
- Working directory is typically at /mnt/host/path/to/project
- Changes to files under /mnt/host are immediately visible on the host
- Some files may be mounted read-only
- Your Go version may not be recent enough for a project, prefer to use `GOTOOLCHAIN=auto` so it can download it

## Docker Support

Docker is supported in this VM. To install and use Docker:

```bash
# Install Docker
apk add docker

# Start Docker daemon
rc-service docker start

# Verify Docker is running
docker info

# Test with a container
docker run --rm alpine echo "Hello from Docker"
```

**Technical notes:**
- Docker uses the `overlay2` storage driver (kernel overlay module)
- If overlay isn't available, `fuse-overlayfs` is installed as a fallback
- Docker networking uses `iptables-legacy` (not nftables)
- Required kernel modules are automatically loaded at boot
