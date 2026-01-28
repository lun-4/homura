# 9pfuse - FUSE 9P Client

A FUSE filesystem driver that connects to 9passthrough servers.

## Overview

9pfuse is a FUSE (Filesystem in Userspace) driver that implements a 9P2000.L client. It mounts a remote 9P filesystem locally using FUSE, providing better control over caching and enabling advanced features like mmap support.

## Usage

```bash
# Mount with defaults (10.0.2.2:5640 -> /mnt/host)
9pfuse

# Custom server and mountpoint
9pfuse -server 10.0.2.2:5640 -mount /mnt/host

# Enable debug logging
9pfuse -debug
```

## Architecture

- `main.go`: Entry point and mount logic
- `client.go`: 9P client implementation using github.com/hugelgupf/p9
- `fuse.go`: FUSE filesystem operations

## Implementation Status

### Phase 1: Basic Functionality ✓
- [x] Connect to 9passthrough server via TCP
- [x] Implement basic file operations (read, write, open, close)
- [x] Directory listing
- [x] File attributes (getattr)
- [x] Write-through caching

### Phase 2: mmap Support (Planned)
- [ ] Extended 9P protocol with mmap operations
- [ ] MmapRegister/MmapBulkRead/MmapWrite handlers
- [ ] Cache invalidation on host changes
- [ ] Bidirectional sync

## Deployment

The binary is automatically built and deployed to VM images during the build process. It will replace the kernel v9fs mount in future VM versions.

## Testing

Basic testing can be done by connecting to a running 9passthrough server:

```bash
# Start server (on host)
9passthrough /path/to/expose

# Mount (in guest or anywhere)
mkdir -p /tmp/test
9pfuse -server localhost:5640 -mount /tmp/test

# Test operations
ls /tmp/test
cat /tmp/test/somefile
```
