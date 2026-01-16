# homura

claude code web for terminal addicts - now using git worktrees!

Manage multiple concurrent working directories within a Git repository using native git worktrees.
Much faster than full copies, and changes are automatically tracked in the same repository.

## how get

```sh
git clone https://github.com/lun-4/homura
cd homura
go build -o homura ./cmd/homura

# do whatever you want
mv ./homura ~/.local/bin
```

## how use

```sh
cd shit

# create a worktree in .homura/fix-indices/ with a new branch
homura clone fix-indices

# list all worktrees
homura ls

# running `homura clone` sets a default branch to the newly created one.
homura sh [branch]

# in the inner shell, you can do whatever you want.
# it's a git worktree - same repo, different working directory
# and that includes running multiple copies of claude
claude

# since it's a worktree, commits go to the same repo!
# no need to sync via remotes
git add ...
git commit ...
git push ...

# exit the homura shell, putting you back to your main shell
exit

# remove the default worktree
# if there are uncommitted changes, this will fail unless you add `-f`
homura rm
```

## benefits over full copies

- **Fast creation** - worktrees are nearly instant, no file copying
- **Shared .git** - all worktrees share the same git objects
- **Easy merging** - branches are in the same repo, just `git merge`
- **Disk efficient** - only working files are duplicated, not git history
