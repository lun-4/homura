# homura

claude code web for terminal addicts - uses git worktrees for fast, disk-efficient branching

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
cd myrepo

# create a worktree at .homura/fix-indices/
# injects some prompting into the worktree's CLAUDE.local.md such that claude should
# just operate inside the worktree (if you want full isolation guarantees, look into `homura vm`)
homura clone fix-indices

# list all branches. you can create more than one concurrently
homura ls

# running `homura clone` sets a default branch to the newly created one.
homura sh [branch]

# in the inner shell, you can do whatever you want.
# this is a git worktree sharing the same .git
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

## `homura vm`

this is my freaky answer to sandboxing. there are issues with other sandboxing solutions
(claude code's bwrap-based sandbox, the remote solutions like exe.dev/sprites/shellbox, etc)
and i'll definitely write an article on it, but for now this is what i got.

`homura vm` is a little tool that will:
- download a linux kernel from Alpine
- download some kernel modules to make a functional VM
- download and assemble an Alpine root fs with Docker
- repackage it all together into an ext4 filesystem image
- granular and dynamic mirroring of the host filesystem into the guest
- make QEMU start with that image

the reasons why those are things that i have to do would be best described in an article, for now here's the setup

NOTE: by default, the current paths are shared with the guest:
- `<cwd>:rw`
- `~/.claude.json:rw`
- `~/.claude:rw`

this lets claude to be run inside the system without having to re-login, a truly ephemeral vm with just what it needs.

```sh
make

# virtio is the default guest fs share type due to perf, you will need to build this
git clone https://github.com/lun-4/virtiofsd
cd virtiofsd && cargo build --release --features http-control
cp ./target/release/virtiofsd ~/.cache/homura/bin/virtiofsd
```

and how to use it

```sh
cd myrepo

homura clone fix-indices

# automatically takes the default branch
homura vm
# OR select your branch
homura vm fix-indices

# get a separate tmux pane
cd myrepo

# and now you can enter the vm!
homura vm ssh

# the host fs gets shared under /mnt/host
cd /mnt/host/home/luna/path/to/myrepo

# claude is preinstalled
claude
```

### vm.json

homura will check `~/.config/homura/vm.json` and you can define things here:
- `allowPaths` is a list of file paths that will be automatically exposed to the guest on vm setup
- `snapshot` is a list of paths that will be snapshotted daily once you start a vm, this is a best-effort snapshot (archives the respective folders in a single .tar)

```json
{
  "configVersion": 1,
  "allowPaths": [
    "/home/luna/.config/homura/custom-vm-bin:ro",
  ],
  "snapshot": [
    "/home/luna/.claude",
    "/home/luna/.claude.json"
  ]
}
```


### custom dockerfile

if you want to install more packages into the base image, create `~/.config/homura/Dockerfile.custom`, an example of mine:

```dockerfile
FROM homura-vm-alpine-base:v23

RUN apk add --no-cache vim tmux ripgrep
RUN apk update
RUN apk add elixir erlang erlang-dev git sqlite sqlite-dev build-base go
RUN mix local.hex --force
RUN mix local.rebar --force

RUN echo 'export PATH="/mnt/host/home/luna/.config/homura/custom-vm-bin:$PATH"' >> /etc/profile
RUN echo 'set -gx PATH /mnt/host/home/luna/.config/homura/custom-vm-bin $PATH' >> /root/.config/fish/config.fish
```

