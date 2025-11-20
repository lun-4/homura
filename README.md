# homura

claude code web for terminal addicts that also really don't want to deal with git submodules or worktrees

(also because CC web does not support setting a docker image as env, and installing Elixir on it was a horror experience
which also didn't work. so fuck it)

_lol_

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
# it's a full clone of your cwd but in a folder inside <repo>/.homura
# and that includes running multiple copies of claude
claude

# since its a full copy of cwd, that includes committing, pushing, etc.
# should all work in the inner "clone"
git add ...
git commit ...
git push ...

# exit the homura shell, putting you back to your main shell
exit

# remove the default branch
# if there are uncomitted changes, this will fail unless you add `-f`
homura rm
```

## drawbacks

- they are full copies, so work done on them must be synced via a git remote.
   you may be able to do `git remote add <NAME> <PATH>` and then merge locallly
   without having to go through your forge of choice.
   this may be a default `homura` command, don't know how well i'd use that yet.
