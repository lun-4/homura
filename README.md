# homura

claude code web for terminal addicts that also really don't want to deal with git submodules or worktrees

(also because CC web does not support setting a docker image as env, and installing Elixir on it was a horror experience
which also didn't work. so fuck it)

_lol_

**Note:** This version uses git worktrees internally for faster cloning and better disk efficiency.
You don't need to know anything about worktrees - homura handles it for you!

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

# copy current cwd to .homura/fix-indices/
homura clone fix-indices

# list all branches. you can create more than one concurrently
homura ls

# running `homura clone` sets a default branch to the newly created one.
homura sh [branch]

# in the inner shell, you can do whatever you want.
# this is a complete copy of your cwd
# and that includes running multiple copies of claude
claude

# since it's a worktree, commits go to the same repo!
# no need to sync via remotes
git add ...
git commit ...
git push ...

# exit the homura shell, putting you back to your main shell
exit

# remove the default branch
# if there are uncommitted changes, this will fail unless you add `-f`
homura rm
```
