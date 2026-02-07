FROM alpine:3.21

RUN apk add --no-cache openrc openssh curl git fish htop bash helix nodejs go python3 cmake \
    && sed -i 's|^root:.*:/bin/sh$|root:x:0:0:root:/root:/usr/bin/fish|' /etc/passwd

# get latest go version
RUN go install golang.org/dl/go1.25.5@latest
RUN ~/go/bin/go1.25.5 download

# Enable services for boot
RUN rc-update add sshd default
RUN rc-update add devfs boot
RUN rc-update add procfs boot
RUN rc-update add sysfs boot
RUN rc-update add hostname boot

# Configure network interface with static IP (QEMU user-mode uses 10.0.2.0/24)
RUN mkdir -p /etc/network && printf 'auto lo\niface lo inet loopback\n\nauto eth0\niface eth0 inet static\n    address 10.0.2.15\n    netmask 255.255.255.0\n    gateway 10.0.2.2\n' > /etc/network/interfaces

# Configure SSH - key auth only
RUN sed -i 's/#PermitRootLogin.*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config

# Setup root SSH directory
RUN mkdir -p /root/.ssh && chmod 700 /root/.ssh

# Inject SSH public key at build time
ARG SSH_PUB_KEY
RUN echo "$SSH_PUB_KEY" > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys

# Fix /var/empty permissions for sshd privilege separation
RUN mkdir -p /var/empty && chown root:root /var/empty && chmod 755 /var/empty

# Set hostname
RUN echo "homura-vm" > /etc/hostname

# Install Claude Code
RUN curl -fsSL https://claude.ai/install.sh | bash
RUN ~/.local/bin/claude update

# remove claude code files, make sure this install is clean
# because we'll symlink with the host later
RUN rm -frv ~/.claude
RUN rm -fv ~/.claude.json
RUN rm -fv ~/.claude.json.backup

# Force OpenRC to run in default mode (not container mode)
# The rootfs is built in Docker, so /.dockerenv exists and OpenRC would detect [DOCKER]
RUN sed -i 's/^#rc_sys=.*/rc_sys=""/' /etc/rc.conf

# Disable all TTYs - we use SSH only, no console login needed
RUN sed -i '/^tty[0-9]/d' /etc/inittab

# Add ~/.local/bin to PATH and set high file descriptor limit for all shells
# (default 1024 is too low for many tools)
RUN echo 'export PATH="$HOME/.local/bin:$PATH"' >> /etc/profile && \
    echo 'ulimit -n 524288' >> /etc/profile

# Add ~/.local/bin to PATH and set file descriptor limit for fish shell
RUN mkdir -p /root/.config/fish && \
    echo 'set -gx PATH $HOME/.local/bin $PATH' >> /root/.config/fish/config.fish && \
    echo 'ulimit -n 524288' >> /root/.config/fish/config.fish

# Build and install 9pvm-request
COPY 9pvm-request /tmp/9pvm-request
RUN cd /tmp/9pvm-request && \
    go build -o /usr/local/bin/9pvm-request . && \
    rm -rf /tmp/9pvm-request

# Build and install test-fs
COPY test-fs /tmp/test-fs
RUN cd /tmp/test-fs && \
    go build -o /usr/local/bin/test-fs . && \
    rm -rf /tmp/test-fs

# Build and install 9pfuse
COPY 9pfuse /tmp/9pfuse
RUN cd /tmp/9pfuse && \
    go build -o /usr/local/bin/9pfuse . && \
    rm -rf /tmp/9pfuse

# Install FUSE kernel module and tools
# Note: alpine-virt kernel modules are not packaged separately
# We'll copy the fuse module from /lib/modules during build
RUN apk add --no-cache fuse3 fuse3-libs

# Install Docker dependencies (so `apk add docker` works cleanly)
# - fuse-overlayfs: fallback storage driver when overlay kernel module isn't available
# - iptables-legacy: Docker networking compatibility (nftables not supported in this kernel)
RUN apk add --no-cache fuse-overlayfs iptables-legacy

# Configure iptables to use legacy backend (required for Docker)
# The nftables backend requires kernel modules we don't have
RUN ln -sf /usr/sbin/iptables-legacy /usr/sbin/iptables && \
    ln -sf /usr/sbin/ip6tables-legacy /usr/sbin/ip6tables

# Enable local service for 9p mount
RUN rc-update add local default
