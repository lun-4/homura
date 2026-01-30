# Homura VM Environment

You are running inside a sandboxed Alpine Linux VM managed by homura. You have root permissions within this VM.

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
