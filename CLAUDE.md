# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

homura is a CLI tool for managing git worktrees. It creates worktrees in `.homura/<branch-name>/` directories, allowing you to work on multiple branches concurrently. Each worktree shares the original repository's `.git` object store but has its own checked-out branch, so you can run multiple instances of Claude Code, commit, push, and work independently.

## Build and Run

```bash
# Build
make

# Run directly
go run ./cmd/homura <command>

# Install locally
mv ./homura ~/.local/bin
```

## Commands

```bash
# Create a worktree at .homura/<branch-name>/ and set as default.
# If the branch already exists, checks it out; otherwise creates a new branch.
# Optional [base] forks a NEW branch from that commit-ish (branch/tag/commit)
# instead of current HEAD; it errors if the branch already exists.
homura clone <branch-name> [base]

# Open shell in worktree (uses default branch if not specified)
homura sh [branch-name]

# List all worktrees and show default branch
homura ls

# Remove worktree (uses default branch if not specified)
homura rm [branch-name]
homura rm -f [branch-name]  # Force removal even with uncommitted changes

# Start a VM for the worktree, detached under the homura daemon (default).
# Streams build/boot progress, then returns; the VM keeps running.
# --here targets the current working directory (no worktree required).
homura vm [branch-name]
homura vm --here            # VM for the current directory, no branch resolution
homura vm --fg [branch-name]  # Old behavior: QEMU chained to this terminal

# SSH into a running VM
homura vm ssh [branch-name]
homura vm ssh --here         # VM for the current directory

# Run a command in the target worktree's VM (starts the VM if needed, then
# runs the command with cwd set to the worktree copy at /mnt/host<path>).
# Requests a pty (-t) when stdin is interactive, so TUIs like `claude` work.
# --here boots/runs in the VM for the current directory (e.g. boot-and-run make).
homura vm run [branch|vmid] -- <command>...
homura vm run --here -- make   # boot current dir's VM, then run make
#   homura vm run -- claude          # default worktree's VM, interactive
#   homura vm run mybranch -- ls -la # a specific branch's worktree
#   homura vm run 3 -- pwd           # a running VM by slot number

# Attach an interactive serial console to a detached VM (Ctrl-] to detach).
# The serial console is also always captured to
# ~/.cache/homura/logs/console-slot<N>-<timestamp>.log, attached or not.
homura vm attach [branch-name]
homura vm attach --here      # attach to the VM for the current directory

# Stop a running VM and clean up (slot, sockets, ephemeral disk; console log survives)
homura vm stop [branch-name]
homura vm stop --here        # stop the VM for the current directory

# List running VMs (shows slot, SSH port, console log path)
homura vm ls
```

## VM Customization

You can customize the VM image by creating a custom Dockerfile at `~/.config/homura/Dockerfile.custom`:

```bash
# Create custom Dockerfile
mkdir -p ~/.config/homura
vim ~/.config/homura/Dockerfile.custom
```

Example customizations:

```dockerfile
FROM homura-vm-alpine-base:v4

# Add packages
RUN apk add --no-cache vim tmux ripgrep

# Install Python packages
RUN pip install --break-system-packages anthropic

# Set environment variables
ENV MY_VAR=value

# Configure shell (fish is default)
RUN echo 'set -gx MY_VAR value' >> /root/.config/fish/config.fish
```

The custom image is automatically built when you run `homura vm`. Images are cached based on file content (MD5 hash), so rebuilds only happen when you modify the Dockerfile.

**Important:** The FROM line must match the current homura version. When homura is updated and the version changes, you must update the FROM line in your Dockerfile.custom or you'll get a version mismatch error with instructions on how to fix it.

**Note:** You cannot use `COPY` in custom Dockerfiles to copy files from the host. Use 9p mounts instead:
```bash
homura 9p expose /path/to/files
# Files accessible at /mnt/host inside VM
```

## Persistent Path Configuration

You can configure paths to be automatically exposed every time a VM starts by creating `~/.config/homura/vm.json`:

```json
{
  "configVersion": 1,
  "allowPaths": [
    "/home/luna/.config/fish:ro",
    "/home/luna/projects:rw",
    "/home/luna/bin"
  ]
}
```

**Path format:**
- `/path:ro` - expose as read-only
- `/path:rw` - expose as read-write (explicit)
- `/path` - expose as read-write (default)

**Note:** The current working directory is always exposed as read-write, regardless of this config. The `allowPaths` setting adds *additional* persistent paths.

## VM State Directory

Per-VM state (the 10G ephemeral rootfs disk, passt/virtiofs sockets, generated SSH keys) lives in a `homura-vm-*` directory under the system temp dir by default. If `/tmp` is tmpfs, every block the guest writes to its disk becomes resident RAM — configure a disk-backed location to run multiple VMs without RAM pressure:

```json
{
  "configVersion": 1,
  "stateDir": "/home.orig/luna/homura-vms"
}
```

The `HOMURA_VM_STATE_DIR` environment variable overrides the config. Per-VM `homura-vm-*` dirs now live on disk (persist across reboots) and are removed on `homura vm stop` and by the stale-slot GC on crash / VM start / `homura vm ls`.

## VM Cache Directory

By default homura keeps its cache tree (VM images, `ssh_host_keys/`, `bin/`, `src/`, `snapshots/`, `logs/`) under `~/.cache/homura`. You can relocate the entire tree with the `cacheDir` key, accepting an absolute path or a `~/...` path:

```json
{
  "configVersion": 1,
  "cacheDir": "/var/cache/homura",
  "stateDir": "/home.orig/luna/homura-vms"
}
```

Or a home-relative path:

```json
{
  "configVersion": 1,
  "cacheDir": "~/homura-cache"
}
```

`cacheDir` is resolved with `filepath.Abs` and, when relative, against the current working directory; a leading `~/` is expanded to the user's home. When the key is absent, the default `~/.cache/homura` is used, so existing setups are unaffected.

**Makefile caveat:** `make 9p` / `make virtiofs` / `make clean` are hardcoded to `~/.cache/homura`. The `bin/9passthrough`, `bin/virtiofsd`, and `src/*` artifacts they produce still land in `~/.cache/homura`. If you set a custom `cacheDir`, you must build those targets pointed at the override (or copy the artifacts there) — otherwise the VM will fail to find `bin/9passthrough` / `bin/virtiofsd` / the `src/*` guest binaries.

## Docker Support

Docker is supported inside homura VMs. The VM includes all necessary kernel modules and dependencies.

### Quick Start

```bash
# Inside the VM:
apk add docker
rc-service docker start
docker run --rm alpine echo "Hello from Docker"
```

### Technical Details

- **Storage driver:** Docker uses `overlay2` by default (kernel overlay module is loaded at boot)
- **Fallback:** `fuse-overlayfs` is pre-installed if overlay isn't available
- **Networking:** Uses `iptables-legacy` (nftables kernel support not included)
- **Boot modules:** Required kernel modules (overlay, bridge, veth, netfilter) are automatically loaded via `/etc/local.d/01docker-modules.start`

### Custom Dockerfile for Persistent Docker

To have Docker pre-installed and auto-started in every VM, create `~/.config/homura/Dockerfile.custom`:

```dockerfile
FROM homura-vm-alpine-base:v25

# Install and enable Docker
RUN apk add --no-cache docker docker-cli-buildx && \
    rc-update add docker default
```

## 9p Filesystem Passthrough

homura VMs support 9p filesystem passthrough, allowing the VM to access host directories securely.

### Usage

**Auto-exposed directory:**
- The working directory is automatically exposed at `/mnt/host` in the VM
- Created/modified files are immediately visible on both sides

**Expose additional paths from host:**
```bash
homura 9p expose /path/to/directory
homura 9p list  # View all exposed paths
```

**Request paths from VM:**
```bash
# Inside the VM:
9pvm-request /home/luna/projects/foo

# On host (approve the request):
homura 9p req list
homura 9p req approve req-<id>
```

**Management commands:**
```bash
homura 9p status              # Show server info (current dir VM)
homura 9p -d /path/to/vm status  # Show status for specific VM
homura 9p unexpose /path      # Remove exposed path
homura 9p req deny <id>       # Deny a VM request
```

**Targeting VMs:**
- By default, commands target the VM in the current working directory
- Use `-d <directory>` to target a different VM
- Example: `homura 9p -d /tmp/myproject expose /home/luna/data`

### How It Works

- VMs mount 9p filesystem automatically at `/mnt/host` (via ./guest/9pfuse, has to be FUSE so that mmap() support exists instead of relying on linux kernel 9p mounting)
- Uses Plan 9 filesystem protocol over TCP (port 5640)
- Sparse visibility: only exposed paths are visible (others return ENOENT)
- Token authentication prevents unauthorized access
- Request approval system for VM-initiated path additions

### Security Model

- Only explicitly exposed paths are accessible
- Non-exposed paths are completely hidden from the VM
- Ancestor directories are synthetic (read-only, show only exposed children)
- VM path requests require host approval
- Full read/write access to exposed paths

## VM Boot Scripts

The VM uses OpenRC's `local` service to run scripts at boot. Scripts are located in `/etc/local.d/` and log to `/var/log/`.

### Scripts

| Script | Log File | Purpose |
|--------|----------|---------|
| `01docker-modules.start` | `/var/log/docker-modules.log` | Loads kernel modules for Docker (overlay, bridge, veth, netfilter) |
| `9pmount.start` | `/var/log/9pmount.log` | Mounts 9p FUSE filesystem at `/mnt/host`, symlinks `~/.claude` and `~/.claude.json` from host |
| `swap.start` | `/var/log/swap.log` | Creates and enables 1GB swap file at `/var/swap` |

### Debugging Boot Issues

```bash
# Inside VM, check boot script logs:
cat /var/log/docker-modules.log
cat /var/log/9pmount.log
cat /var/log/swap.log
```

The scripts are generated in `internal/vm/build.go` during rootfs creation.

## VM Architecture

This section provides technical details about how the VM system works internally.

### QEMU Configuration

VMs are launched with QEMU using direct kernel boot (no BIOS/firmware):

- **Machine type:** q35 (modern PCI-capable)
- **Virtualization:** KVM with host CPU passthrough
- **Resources:** 4GB RAM, 4 CPUs
- **Devices:**
  - `virtio-blk-pci` for rootfs (ext4 on `/dev/vda`)
  - `virtio-net-pci` connected via passt Unix socket
  - Serial console (`-nographic`): in daemon mode (default), a unix-socket chardev at `<stateDir>/console.sock` with QEMU-native capture to a logfile (`-chardev socket,...,logfile=...`); with `--fg`, plain `-serial stdio`

**Kernel command line parameters:**
```
earlyprintk=ttyS0 console=ttyS0
root=/dev/vda rootfstype=ext4 rw acpi=off
p9.token=<token> p9.port=<controlPort> p9.listenport=<9pPort>
host.home=/home/user
```

Relevant files: `internal/vm/qemu.go`, `internal/vm/vm.go`

### Image Build Pipeline

The build system creates VM images in `~/.cache/homura/v{VERSION}/vm-images/alpine-{ALPINE_VERSION}/`:

1. **Alpine download:** Fetches kernel (`vmlinuz-virt`), initramfs, and modloop from Alpine CDN

2. **Custom initramfs creation:**
   - Extracts Alpine's original initramfs
   - Injects custom init script from `internal/vm/resources/init`
   - Adds kernel modules from modloop: `virtio_blk`, `ext4`, `virtio_net`, `9p`, `9pnet`, `fuse`
   - Creates cpio archive with gzip compression

3. **Docker base image** (`homura-vm-alpine-base:v{VERSION}`):
   - Built on Alpine 3.21
   - Packages: openrc, openssh, fish, git, go, python3, cmake, build tools
   - Network: static IP `10.0.2.15/24`
   - SSH: key-only auth
   - OpenRC configured for non-container mode

4. **Custom Dockerfile support:**
   - Optional `~/.config/homura/Dockerfile.custom`
   - Hash-based caching (rebuilds only when content changes)

5. **Rootfs image creation:**
   - 1.5GB ext4 filesystem
   - Docker/Podman exports container to tar
   - Mounted with `fuse2fs` for modification
   - Boot scripts embedded in `/etc/local.d/`
   - Guest binaries copied to `/usr/local/bin/`

**Container engine detection:** `buildRootfs` calls `detectContainerCmd` to choose the CLI. Order: `$HOMURA_CONTAINER_CMD` if set, else `docker` if in `PATH`, else `podman`. If neither is present the build aborts with a clear error. Set `HOMURA_CONTAINER_CMD=podman` to force podman on hosts that have both.

Relevant files: `internal/vm/build.go`, `internal/vm/resources/Dockerfile`, `internal/vm/resources/init`

### Networking (Passt)

Passt provides user-space networking without root privileges:

- **Guest IP:** `10.0.2.15/24`
- **Gateway:** `10.0.2.2` (host side, used for 9p connections)
- **Port allocation:** 10 ports per VM slot
  - First port → SSH (guest port 22)
  - Remaining 9 ports → 1:1 pass-through

**Slot-based port mapping:**
- Slot 1: `127.0.0.1:10000-10009`
- Slot 2: `127.0.0.2:10010-10019`
- Up to 254 concurrent VMs

Relevant files: `internal/vm/passt.go`

### State Management & Slot Allocation

Global VM state is stored in `/tmp/homura_vm_state.db` (SQLite):

```sql
vm_slots:
  slot_number     -- 1-254, unique IP/port range
  ip_address      -- 127.0.0.X (X = slot number)
  port_start/end  -- allocated port range
  vm_pid          -- PID of the owning homura process (daemon, or the fg homura for --fg VMs)
  passt_socket_path
  working_dir     -- directory where VM was started
  ninep_pid       -- 9passthrough process ID
  ninep_control_socket/port
  created_at
  qemu_pid        -- QEMU process ID (filled in after launch)
  console_socket  -- serial console unix socket ("" for --fg VMs; how attach/stop tell modes apart)
  console_log     -- persistent console log path in ~/.cache/homura/logs/
```

Slot allocation uses `BEGIN IMMEDIATE` transactions to prevent race conditions. Stale slots are auto-cleaned by checking if the owning PID is still alive (and their `homura-vm-*` state dirs removed).

`vm ssh`, `vm ls`, `vm attach`, and the `9p` commands read this DB directly — they never need the daemon.

Relevant files: `internal/vm/globalstate.go`

### VM Daemon

By default `homura vm` runs VMs detached under a single central daemon instead of chaining QEMU to the terminal (`--fg` restores that).

**Lifecycle:**
- Any `homura vm` auto-spawns `homura daemon run` (hidden command) via re-exec with `Setsid` if no daemon answers. A `flock` on `<runtime>/daemon.lock` makes concurrent spawns converge on one daemon (losers exit 0).
- Runtime dir: `$XDG_RUNTIME_DIR/homura` (fallback `/tmp/homura-<uid>`), holding `daemon.sock` (0600) and `daemon.lock`.
- The daemon idle-exits 60 seconds after the last VM stops (respawn is cheap). SIGTERM/SIGINT gracefully stops all VMs first.
- Daemon log: `~/.cache/homura/logs/daemon.log`.

**RPC protocol** (newline-delimited JSON over the unix socket, one connection per request, same style as the 9passthrough control socket): `ping`, `start-vm` (streams `{"event":"log"}` frames with build/boot progress before the final result — this is how `homura vm` still shows image-build output), `stop-vm`, `list-vms`, `shutdown`.

**Process ownership:** the daemon is the parent of QEMU/passt/virtiofsd/9passthrough, all spawned with `Pdeathsig: SIGTERM` so a daemon crash doesn't leak them; a supervision goroutine per VM reaps QEMU and runs cleanup on exit. `vm_slots.vm_pid` is the daemon's PID, so the existing stale-slot GC handles daemon death.

**Serial console:** QEMU writes the console to `<stateDir>/console.sock` (chardev with `logfile=`), so kernel output is always captured to `~/.cache/homura/logs/console-slot<N>-<timestamp>.log` — attached or not, surviving VM cleanup. `homura vm attach` connects directly to the socket (raw TTY, Ctrl-] detaches); an advisory flock on `attach.lock` prevents a second attach from silently hanging (QEMU socket chardevs serve one client).

Relevant files: `internal/daemon/` (paths, protocol, server, client), `internal/commands/vm_attach.go`, `internal/commands/vm_stop.go`

### 9p Filesystem (Host Side)

The `9passthrough` binary (`~/.cache/homura/bin/9passthrough`) provides the host-side 9p server:

**Architecture:**
- TCP server for 9p protocol (auto-allocated port)
- Unix socket for host-side control commands (like exposing new paths)
- TCP control server for VM requests (token-authenticated), this is done to prevent VMs from requesting mounts under other VMs contexts
- NOTE: VMs can brute-force 9p mounts to other 9passthrough daemons as 9p has no builtin auth (this may change)
- Uses `github.com/hugelgupf/p9` library

**Path visibility states:**
- **NotVisible:** Returns ENOENT (path not exposed)
- **VirtualAncestor:** Synthetic directory (ancestor of exposed paths, read-only)
- **Exposed:** Real path on host filesystem

**Initially exposed paths:**
1. Working directory (read-write)
2. `~/.claude.json` if exists (read-write)
3. `~/.claude/` if exists (read-write)
4. `~/.config/homura/CLAUDE.md` if exists (read-only)
5. Custom exposed paths from `~/.config/homura/vm.json` (this is configured by the user)

**Token authentication:**
- 256-bit random token generated at startup
- Written to `/tmp/9p-token-{PID}` on the host with control port and 9p port
- VM reads token from kernel cmdline parameter

Relevant files: `9passthrough/`

### 9p Filesystem (Guest Side - 9pfuse)

The `9pfuse` binary provides FUSE-based 9p mounting in the guest:

**Why FUSE instead of kernel 9p:**
- Linux kernel 9p driver doesn't support `mmap()` properly
- FUSE allows full mmap support (required for many tools)

**Features:**
- Mount point: `/mnt/host`
- Connects to host at `10.0.2.2:$P9_LISTENPORT`
- Attribute caching with TTL (reduces round-trips)
- `FOPEN_KEEP_CACHE` for read-only files
- Tracing: `SIGUSR1` dumps stats, `SIGUSR2` resets

**mmap protocol extension (custom 9p messages 200-204):** (NOTE: this is not fully used, fuse does the job well enough)
- `MmapRegister`: Client registers file for mmap
- `MmapBulkRead`: Efficient initial read of mmapped region
- `MmapWrite`: Guest writes to mmapped region
- `MmapNotify`: Host notifies guest of external changes
- `MmapUnregister`: Client done with mmap

Relevant files: `guest/9pfuse/`

### Boot Sequence

**1. Initramfs phase** (`internal/vm/resources/init`):
```
1. Create busybox symlinks
2. Mount proc, sysfs, devtmpfs
3. Load kernel modules (virtio, ext4, 9p, fuse)
4. Wait for /dev/vda (rootfs block device)
5. Mount ext4 rootfs to /newroot
6. Configure network (eth0 = 10.0.2.15/24)
7. Set hostname
8. switch_root to /newroot, exec /sbin/init
```

**2. OpenRC boot** (rootfs `/etc/local.d/*.start`):
```
9pmount.start:
  1. Parse kernel cmdline for p9 parameters
  2. Load FUSE module
  3. Start 9pfuse daemon
  4. Wait for /mnt/host mount
  5. Symlink ~/.claude.json and ~/.claude/ from host
  6. Append user's CLAUDE.md to VM's CLAUDE.md (this is important as the guest VM has tools like 9pvm-request)

swap.start:
  1. Create 1GB sparse file at /var/swap
  2. Enable swap
```

### Guest Binaries

Built and embedded in rootfs at `/usr/local/bin/`:

| Binary | Purpose |
|--------|---------|
| `9pfuse` | FUSE 9p client for mounting `/mnt/host` |
| `9pvm-request` | Request additional paths from inside VM |
| `test-fs` | Filesystem validation tool |

### Ephemeral Disk

Each VM gets a fresh rootfs:
1. Sparse copy of base image (`cp --sparse=always`)
2. Resize to 10GB (`truncate -s 10G`)
3. Expand filesystem (`resize2fs`)
4. Deleted on VM shutdown

No state persists between VM runs.

### SSH Access

- **Port:** First port in slot range (e.g., `127.0.0.1:10000` for slot 1)
- **User:** root
- **Auth:** Public key from `~/.ssh/id_ed25519.pub` (falls back to RSA/ECDSA)
- **Host keys:** Persistent at `~/.cache/homura/ssh_host_keys/`

### Snapshots (Optional)

Configure in `~/.config/homura/vm.json`:
```json
{
  "snapshot": ["/home/user/projects"],
  "maxSnapshots": 7
}
```

- Snapshots paths before each VM start
- Max 1 snapshot per day
- Stored at `~/.cache/homura/snapshots/`
- Auto-cleanup removes oldest beyond limit

Relevant files: `internal/vm/snapshot.go`

## Architecture

### State Management
- State is stored in SQLite at `.homura/state.db` in the repository root
- Uses a migration system (see `internal/config/state.go`) - add new migrations to the `migrations` slice
- Current state tracks the default branch name (used when branch arg is omitted)
- State is loaded/saved via `config.LoadState()` and `config.SaveState()`

### Directory Structure
- All worktrees live under `.homura/<branch-name>/` in the repository root
- Each worktree is a `git worktree` of the parent repo with the specified branch checked out (shares the parent's `.git` object store)
- `git.GetRepoRoot()` resolves a `.homura/<branch>/` working directory back to the parent repo root, so homura commands operate on the main repo rather than nesting `.homura` directories

### Key Packages
- `cmd/homura/main.go`: CLI entry point using cobra for command routing
- `internal/commands/`: Command implementations (clone, sh, rm, ls)
- `internal/config/state.go`: SQLite state management with migrations
- `internal/git/git.go`: Git operations (status checks, branch checkout, path utilities)

### Core Behaviors
- `clone` runs `git worktree add` for the branch and sets it as default. If the branch already exists, it checks it out (`git worktree add <path> <branch>`); otherwise it creates a new branch (`git worktree add -b <branch> <path>`). Errors if `.homura/<branch>/` already exists on disk, or if git refuses because the branch is already checked out in another worktree. Also drops a `CLAUDE.local.md` into the worktree and adds it to the worktree's `.git/info/exclude`
- `sh` spawns `$SHELL` in the worktree directory (defaults to `/bin/sh`)
- `rm` runs `git worktree remove`; it prevents removal of worktrees with uncommitted changes unless `-f` is used
- All commands use slog for structured logging to stderr

### SQLite Usage
- State database uses a simple key-value table
- Migration system tracks applied schema changes in `migration_state` table
- Database connections are short-lived (opened and closed within each operation)

### VM Implementation Versioning

The VM system uses a versioning scheme to track implementation changes and automatically rebuild images when features are added or changed.

#### How It Works
- Version constant: `VMImplementationVersion` in `internal/vm/version.go`
- Version is included in cache path: `~/.cache/homura/v{VERSION}/vm-images/alpine-{ALPINE_VERSION}/`
- When version changes, cache path changes, automatically triggering a rebuild
- Old versions remain in separate directories (can be cleaned up manually)
- SSH host keys are stored separately at `~/.cache/homura/ssh_host_keys/` (not versioned, persist across version changes)

#### When to Bump the Version

Increment `VMImplementationVersion` in `internal/vm/version.go` when:

1. **Changing embedded resources:**
   - `internal/vm/resources/init` script changes
   - `internal/vm/resources/Dockerfile` changes (new packages, config changes, etc.)

2. **Changing build logic that affects VM images:**
   - Kernel modules loaded in initramfs (`buildInitramfs()`)
   - Rootfs build process (`buildRootfs()`)
   - Network configuration, DNS settings, etc. that get baked into images

3. **Adding VM features that require image rebuild:**
   - New system packages
   - New kernel parameters
   - Changed QEMU configuration that affects guest behavior
   - Changing core homura vm binaries: 9pvm-request, test-fs, 9pfuse

#### When NOT to Bump the Version

Do NOT bump version for:
- Changes to QEMU launch args that don't affect guest (e.g., host-side memory/CPU settings, serial console wiring)
- Changes to the homura daemon (`internal/daemon/`) or the attach/stop commands
- Changes to passt networking host-side configuration
- Changes to VM state management (`globalstate.go`)
- Changes to slot allocation logic
- Code refactoring that doesn't change VM behavior
- Documentation changes

#### Example Workflow

```bash
# Developer adds a new package to Dockerfile
vim internal/vm/resources/Dockerfile
# Add: apk add --no-cache vim

# Bump version in version.go
vim internal/vm/version.go
# Change: const VMImplementationVersion = 1
# To:     const VMImplementationVersion = 2
# Update version history comment

# Commit changes
git add internal/vm/resources/Dockerfile internal/vm/version.go
git commit -m "Add vim to VM image (v2)"

# Next VM launch automatically uses new versioned path
homura vm
# Creates: ~/.cache/homura/v2/vm-images/alpine-3.21/
```

#### Cleaning Up Old Versions

Old version directories remain in `~/.cache/homura/` and can be removed manually:

```bash
# Remove old version
rm -rf ~/.cache/homura/v1/

# Or remove all old versions except current
# (where current is VMImplementationVersion = 2)
rm -rf ~/.cache/homura/v1/
```

#### Version History

Track changes in `internal/vm/version.go` comments:

```go
// Version History:
// 1 - Initial implementation with passt networking
// 2 - Added vim to base image (example)
```

## testing

Run `make test`.
