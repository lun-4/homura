# Homura VM Environment

You are running inside an Ubuntu VM managed by homura.
In this VM, you got root permissions and can install packages.
You're encouraged to install packages in the ephemeral VM to get projects/tasks running, as it wouldn't clutter the host environment.

## Host Filesystem Access

The host filesystem is partially available at `/mnt/host`. Only explicitly exposed paths are accessible.

If a path that is from `/mnt/host` is unaccessible and it would be important to have access to it, you can invoke the following command to request additional paths:

    9pvm-request /path/on/host

Instructions:
- `/path/on/host` is the absolute path on the host filesystem, `/mnt/host/path/on/host` should be mapped as `9pvm-request /path/on/host`.
- This command blocks until the user approves or rejects the request on the host side. Once approved, the path becomes available at `/mnt/host/path/on/host`.

## Environment Notes
- Ubuntu with `apt`
- Running as root user
- Working directory is typically at /mnt/host/path/to/project
- Changes to files under /mnt/host are immediately visible on the host
- Some files may be mounted read-only
- The guest uses `systemd`
- Your Go version may not be recent enough for a project, prefer to use `GOTOOLCHAIN=auto` so it can download it

## Docker Support

Docker is supported in this VM. To install and use Docker:

```bash
# Install Docker
apt-get update
apt-get install -y docker.io

# Start Docker daemon
systemctl enable --now docker.service

# Verify Docker is running
docker info

# Test with a container
docker run --rm alpine echo "Hello from Docker"
```

**Technical notes:**
- Docker uses the `overlay2` storage driver (kernel overlay module)
- If overlay isn't available, `fuse-overlayfs` is installed as a fallback
- Docker networking uses `iptables-legacy` (not nftables)
- Required kernel modules are automatically loaded at boot by `homura-docker-modules.service`
