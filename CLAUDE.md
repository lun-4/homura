# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

homura is a CLI tool for managing temporary git repository copies. It creates isolated copies of your repository in `.homura/<branch-name>/` directories, allowing you to work on multiple branches concurrently without using git worktrees or submodules. Each copy is a full clone where you can run multiple instances of Claude Code, commit, push, and work independently.

## Build and Run

```bash
# Build
go build -o homura ./cmd/homura

# Run directly
go run ./cmd/homura <command>

# Install locally
mv ./homura ~/.local/bin
```

## Commands

```bash
# Clone current repo to .homura/<branch-name>/ and set as default
homura clone <branch-name>

# Open shell in copy (uses default branch if not specified)
homura sh [branch-name]

# List all copies and show default branch
homura ls

# Remove copy (uses default branch if not specified)
homura rm [branch-name]
homura rm -f [branch-name]  # Force removal even with uncommitted changes
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

- VMs mount 9p filesystem automatically at `/mnt/host` (via 9pfuse, has to be FUSE so that mmap() support exists instead of relying on linux kernel 9p mounting)
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

## Architecture

### State Management
- State is stored in SQLite at `.homura/state.db` in the repository root
- Uses a migration system (see `internal/config/state.go`) - add new migrations to the `migrations` slice
- Current state tracks the default branch name (used when branch arg is omitted)
- State is loaded/saved via `config.LoadState()` and `config.SaveState()`

### Directory Structure
- All copies live under `.homura/<branch-name>/` in the repository root
- The `.homura` directory itself is excluded from copies to prevent recursion
- Each copy is a complete clone of the original repository with the specified branch checked out

### Key Packages
- `cmd/homura/main.go`: CLI entry point using cobra for command routing
- `internal/commands/`: Command implementations (clone, sh, rm, ls)
- `internal/config/state.go`: SQLite state management with migrations
- `internal/git/git.go`: Git operations (status checks, branch checkout, path utilities)

### Core Behaviors
- `clone` copies the entire repo excluding `.homura`, checks out the branch, and sets it as default
- `sh` spawns `$SHELL` in the copy directory (defaults to `/bin/sh`)
- `rm` prevents removal of copies with uncommitted changes unless `-f` is used
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

#### When NOT to Bump the Version

Do NOT bump version for:
- Changes to QEMU launch args that don't affect guest (e.g., host-side memory/CPU settings)
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
