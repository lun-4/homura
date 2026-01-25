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
