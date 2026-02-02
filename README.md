# homura

claude code web for terminal addicts w/ git worktrees

problem statement: Claude Code on the Web UI sucks ass.
0. the idea is cool! claude on "YOLO" mode while also being in the isolated env is very cool
1. does not support setting a custom docker image as an environment, so everything must go through Claude
2. impossible to install Elixir on it, it was insane horror and it didn't work
3. very bad latency, general Anthropic UI jank

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

**NOTE** linux only atm

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

### requirements

**system:**
- Linux with KVM support (check with `ls /dev/kvm`)
- internet access (to download Alpine components on first run)

**packages:**
| package | provides | notes |
|---------|----------|-------|
| qemu | `qemu-system-x86_64` | VM emulator |
| passt | `passt` | userspace networking, no root needed |
| docker or podman | `docker`/`podman` | image building (docker tested first) |
| fuse2fs | `fuse2fs`, `fusermount` | ext4 FUSE mounting (usually in `e2fsprogs` or `fuse2fs`) |
| e2fsprogs | `mke2fs`, `resize2fs` | ext4 filesystem tools |
| squashfs-tools | `unsquashfs` | extract Alpine modules |
| kmod | `depmod` | kernel module dependencies |
| coreutils | GNU `truncate`, `cp` | sparse file creation |
| tar, gzip, cpio | archive tools | initramfs building |
| openssh | `ssh-keygen` | VM host key generation |

on arch: `pacman -S qemu-base passt docker e2fsprogs squashfs-tools kmod coreutils tar gzip cpio openssh`

on debian/ubuntu: `apt install qemu-system-x86 passt docker.io e2fsprogs fuse2fs squashfs-tools kmod coreutils tar gzip cpio openssh-client`

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

