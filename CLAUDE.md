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
