# homura

Claude Code tooling AGAINST Mami Tomoe. the bitch.

problem statements:
1. Claude Code on the Web UI sucks ass.
   1. the idea is cool! claude on "YOLO" mode while also being in the isolated cloud env is very cool
   2. does not support setting a custom docker image as an environment, so everything must be installed through Claude
   3. impossible to install Elixir on it, it was insane horror and it didn't work
   4. very bad latency, general Anthropic UI jank
2. Claude Code's local sandboxing sucks the more agency you want to give to Claude.
   1. easy to just say yes all the time
   2. changing policies mid-session must go through Claude Code, or restart it (this is also a problem with bwrap-based solutions)
   3. the combinatorial complexity of letting _any command_ be run is psychologically taxing (you need to keep track of what flags are safe, what isn't)
   4. much more detail in [my post](https://l4.pm/wiki/Personal%20Wiki/AI%20stuff/adventures%20in%20sandboxing.html)

my solutions:
1. easier worktree management, all worktrees stored in `<repo>/.homura/<branch>`, so if you don't like the tool you don't need to reverse engineer it to do your work
2. VM-based sandboxing

## how get

you can run homura without interacting with the `homura vm` subcommand (which needs a lot more dependencies).
think of the `vm` subcommand as a superset of the base featureset.

if you just want worktree management, install `go` and follow these instructions:

```sh
git clone https://github.com/lun-4/homura
cd homura
make

# do whatever you want
mv ./homura ~/.local/bin
```

## how to use

```sh
cd myrepo

# create a worktree at .homura/fix-indices/
# injects some prompting into the worktree's CLAUDE.local.md such that claude should
# just operate inside the worktree (if you want full isolation guarantees, look into `homura vm`)
homura clone fix-indices

# list all branches. you can have more than one branch
homura ls

# running `homura clone` sets a "default branch" internally to the newly created one.
# that means you don't need to give the branch name to some other commands (all optional)
# this is equivalent to `cd .homura/<branch> && $SHELL`
homura sh [branch]

# in the inner shell, you can do whatever you want.
# this is a git worktree sharing the same .git
# and that includes running multiple copies of claude
# (NOTE: merge conflict resolution between multiple agents is left as an exercise to the reader)
claude

# since it's a worktree, commits go to the same repo!
# no need to sync via separate git remotes
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
and i have written an article on it: [adventures in sandboxing](https://l4.pm/wiki/Personal%20Wiki/AI%20stuff/adventures%20in%20sandboxing.html).

`homura vm` is a little tool that will:
- download a linux kernel from Alpine
- download some kernel modules to make a functional VM
- download and assemble an Ubuntu root fs with Docker
- repackage it all together into an ext4 filesystem image
- granular and dynamic mirroring of the host filesystem into the guest (through virtiofs)
  - because of virtiofs you need a high max fd limit. you can do this via a sudo shell alias, as an example `maxfd="sudo -E bash -c 'ulimit -n 524288 && exec sudo -Eu luna fish'"`
- make QEMU start with that image with configured SSH and networking via `passt`
  - each VM gets allocated a 10 port range starting from 10000, so the first VM gets 10000-10009 (10000 being SSH), next VM gets 10010-10019, etc

the reasons why those are things that i do this are in the article, for now here's the setup

### requirements

**system:**
- Linux with KVM support (check with `ls /dev/kvm`)
- internet access (to download Alpine components on first run)

**packages:**
| package | provides | notes |
|---------|----------|-------|
| qemu | `qemu-system-x86_64` | VM emulator |
| passt | `passt` | userspace networking, no root needed |
| docker | `docker` | image building |
| fakeroot | `fakeroot` | fake root ownership for image building |
| e2fsprogs | `mke2fs`, `resize2fs` | ext4 filesystem tools |
| squashfs-tools | `unsquashfs` | extract Alpine modules |
| kmod | `depmod` | kernel module dependencies |
| coreutils | GNU `truncate`, `cp` | sparse file creation |
| tar, gzip, cpio | archive tools | initramfs building |
| openssh | `ssh-keygen` | VM host key generation |

installing those packages on your distro is left as an exercise to the reader

NOTE: by default, the current paths are shared with the guest:
- `<cwd>:rw`
- `~/.claude.json:rw`
- `~/.claude:rw`

this lets claude to be run inside the system without having to re-login, a truly ephemeral vm with just what it needs.

### building

```sh
# while in homura, build if you haven't
make

# virtio is the default guest fs share type due to performance reasons, you will need to build this
# you can clone this anywhere at the moment
# IMPORTANT NOTE: DO NOT USE SYSTEM virtiofsd.
#  homura needs a patched virtiofsd that can do granular sharing:
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
- `allowPaths` is a list of file paths that will be automatically exposed to the guest on vm setup.
  - useful to put some tools or scripts to configure claude properly with yolo mode
  - paths are `<host path>:<ro or rw>`
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
# you'll need to change this whenever i change the base image to ensure the images are recent and prevent rebuilds
# because of this you'll need to remove old images manually. i could add auto cleaning in the future though!
FROM homura-vm-ubuntu-base:v32

RUN apt-get update && apt-get install -y --no-install-recommends \
    vim \
    tmux \
    ripgrep \
    elixir \
    erlang \
    erlang-dev \
    git \
    sqlite3 \
    libsqlite3-dev \
    build-essential \
    golang-go \
    && rm -rf /var/lib/apt/lists/*
RUN mix local.hex --force
RUN mix local.rebar --force

RUN echo 'export PATH="/mnt/host/home/luna/.config/homura/custom-vm-bin:$PATH"' >> /etc/profile
RUN echo 'set -gx PATH /mnt/host/home/luna/.config/homura/custom-vm-bin $PATH' >> /root/.config/fish/config.fish
```
