FROM ubuntu:26.04

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=/root/sdk/go1.25.5/bin:/root/go/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    build-essential \
    ca-certificates \
    cmake \
    curl \
    dbus \
    fish \
    fuse-overlayfs \
    fuse3 \
    git \
    golang-go \
    htop \
    iproute2 \
    iptables \
    iputils-ping \
    kmod \
    nodejs \
    openssh-server \
    procps \
    python3 \
    python3-pip \
    systemd \
    systemd-sysv \
    vim \
    && rm -rf /var/lib/apt/lists/* \
    && usermod -s /usr/bin/fish root

# get latest go version
RUN go install golang.org/dl/go1.25.5@latest
RUN /root/go/bin/go1.25.5 download

# Configure SSH - key auth only
RUN sed -ri 's/^#?PermitRootLogin .*/PermitRootLogin prohibit-password/' /etc/ssh/sshd_config && \
    sed -ri 's/^#?PasswordAuthentication .*/PasswordAuthentication no/' /etc/ssh/sshd_config && \
    sed -ri 's|^#?AuthorizedKeysFile .*|AuthorizedKeysFile .ssh/authorized_keys|' /etc/ssh/sshd_config

# Setup root SSH directory
RUN mkdir -p /root/.ssh && chmod 700 /root/.ssh

# Inject SSH public key at build time
ARG SSH_PUB_KEY
RUN echo "$SSH_PUB_KEY" > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys

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

# Use multi-user.target and suppress getty noise on tty1/ttyS0.
RUN ln -sf /lib/systemd/system/multi-user.target /etc/systemd/system/default.target && \
    ln -sf /lib/systemd/system/ssh.service /etc/systemd/system/multi-user.target.wants/ssh.service && \
    ln -sf /dev/null /etc/systemd/system/getty@tty1.service && \
    ln -sf /dev/null /etc/systemd/system/serial-getty@ttyS0.service

# Ensure systemd and D-Bus do not retain a Docker-generated machine identity.
RUN truncate -s 0 /etc/machine-id && \
    rm -f /var/lib/dbus/machine-id && \
    ln -sf /etc/machine-id /var/lib/dbus/machine-id

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

# Configure iptables to use legacy backend (required for Docker)
# The nftables backend requires kernel modules we don't have
RUN update-alternatives --set iptables /usr/sbin/iptables-legacy && \
    update-alternatives --set ip6tables /usr/sbin/ip6tables-legacy
