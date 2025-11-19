# homura

A CLI tool for managing temporary git repository copies for isolated development work.

## Installation

```bash
go build -o homura ./cmd/homura
```

Then move the binary to somewhere in your PATH:
```bash
sudo mv homura /usr/local/bin/
```

## Usage

### Clone a repository

Copy the current repo to `.homura/<branch-name>/`, including all uncommitted changes:

```bash
homura clone <branch-name>
```

This will:
- Create a full recursive copy including the .git directory
- Include all uncommitted changes
- Checkout to `<branch-name>` in the copy (creates the branch if it doesn't exist)
- Set this as the default branch

### Open a shell in a copy

```bash
homura sh [branch-name]
```

If `branch-name` is not provided, uses the default branch.

### Remove a copy

```bash
homura rm [branch-name]
```

If `branch-name` is not provided, uses the default branch.

If there are uncommitted changes, you'll need to use the `-f` flag:

```bash
homura rm [branch-name] -f
```

### List all copies

```bash
homura ls
```

Shows all branch copies and indicates which one is the default.

## How it works

- **Storage**: Repo copies are stored at `<original-repo>/.homura/<branch-name>/`
- **State tracking**: Per-repo state is stored at `<original-repo>/.homura/state.toml`
- **Default branch**: After cloning, the branch becomes the "default" so it's optional for `sh` and `rm` commands
