package vmlua

import (
	"fmt"
	"strings"
)

// This file ships homura's core "recipes": composable one-liners built on the
// builder primitives. They are versioned via VMImplementationVersion and pinned
// to specific URLs/versions. Any emitted-line change is caught by the golden
// tests in recipes_test.go.

const (
	// ubuntuCodename is the distro release codename used by addDockerUbuntuRepo
	// (matches internal/vm's Ubuntu 24.04 base image).
	ubuntuCodename = "noble"
)

// addDockerUbuntuRepo adds the Docker apt repository and refreshes apt. It does
// not install Docker itself (installDocker is composed by the user in vm.lua).
//
//	RUN curl -fsSL <keyring> -o /usr/share/keyrings/docker.asc && echo 'deb [signed-by=...] ... noble stable' > /etc/apt/sources.list.d/docker.list && apt-get update
func (b *Builder) addDockerUbuntuRepo() {
	b.run(fmt.Sprintf(
		"curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /usr/share/keyrings/docker.asc && echo 'deb [signed-by=/usr/share/keyrings/docker.asc] https://download.docker.com/linux/ubuntu %s stable' > /etc/apt/sources.list.d/docker.list && apt-get update",
		ubuntuCodename))
}

// installHelix downloads a static Helix release (version, e.g. "25.07.1") from
// GitHub and extracts it into /root/helix, then installs a PATH symlink and
// checks the version.
func (b *Builder) installHelix(version string) {
	url := fmt.Sprintf("https://github.com/helix-editor/helix/releases/download/%[1]s/helix-%[1]s-x86_64-linux.tar.xz", version)
	b.run(fmt.Sprintf(
		"mkdir -p /root/helix && curl -fsSL %s -o /tmp/helix.tar.xz && tar -xf /tmp/helix.tar.xz -C /root/helix --strip-components=1 && ln -sf /root/helix/hx /usr/local/bin/hx && rm /tmp/helix.tar.xz && hx --version | grep -qF %q",
		url, version))
}

// InstallGoOpts controls installGo.
type InstallGoOpts struct {
	DownloadViaSystemGo bool
}

// installGo installs the given Go toolchain (version, e.g. "1.24.0") via the
// golang.org/dl helper. downloadViaSystemGo uses the system go binary to run
// the install instead of whatever `go` is on PATH.
func (b *Builder) installGo(version string, downloadViaSystemGo bool) {
	goBin := "go"
	if downloadViaSystemGo {
		goBin = "/usr/bin/go"
	}
	b.run(fmt.Sprintf("%s install golang.org/dl/go%s@latest && go%s download", goBin, version, version))
}

// installElixir installs the hex and rebar package managers via mix. Assumes
// elixir/erlang are already apt-installed.
func (b *Builder) installElixir(hex, rebar bool) {
	var cmds []string
	if hex {
		cmds = append(cmds, "mix local.hex --force")
	}
	if rebar {
		cmds = append(cmds, "mix local.rebar --force")
	}
	if len(cmds) > 0 {
		b.run(strings.Join(cmds, " && "))
	}
}

// installClaude pins the Claude Code CLI (version, e.g. "2.1.233") and verifies
// it.
func (b *Builder) installClaude(version string) {
	b.run(fmt.Sprintf("~/.local/bin/claude update %s && ~/.local/bin/claude --version | grep -qF %q", version, version))
}

// installRust installs the stable Rust toolchain via rustup-init.
func (b *Builder) installRust() {
	b.run("curl -fsSL https://sh.rustup.rs -o /tmp/rustup-init && sh /tmp/rustup-init -y --default-toolchain stable && rm /tmp/rustup-init")
}

// installPolytoken installs polytoken from get.polytoken.dev and symlinks only
// the polytoken cache/config/state dirs to the host (read-write), auto-exposing
// them. It deliberately does NOT expose the whole ~/.cache or ~/.config.
func (b *Builder) installPolytoken() {
	b.run("curl -fsS https://get.polytoken.dev | bash")
	b.run("mkdir -p /root/.local/share")
	rw := &SymlinkOpts{RW: true}
	home := b.hostHome
	b.symlinkFromHost(hostHomeJoin(home, ".cache", "polytoken"), "/root/.cache/polytoken", rw)
	b.symlinkFromHost(hostHomeJoin(home, ".config", "polytoken"), "/root/.config/polytoken", rw)
	b.symlinkFromHost(hostHomeJoin(home, ".local", "share", "polytoken"), "/root/.local/share/polytoken", rw)
}

// installPi installs the pi CLI: fd-find + node/npm, version checks, the pi
// installer script, and a read-write host symlink for ~/.pi. Only the user's
// exposed subdir (~/.pi/agent) is auto-registered in allowPaths — NOT the whole
// ~/.pi.
func (b *Builder) installPi() {
	b.aptInstall("fd-find")
	b.aptInstall("nodejs", "npm")
	b.run("node --version && npm --version")
	b.run("curl -fsSL https://pi.dev/install.sh | sh")
	home := b.hostHome
	// Symlink the whole ~/.pi (so the pi layout is shared) but auto-register only
	// the .pi/agent subdir for exposure.
	b.symlinkFromHost(hostHomeJoin(home, ".pi"), "/root/.pi", &SymlinkOpts{RW: true, NoExpose: true})
	b.extraPaths = append(b.extraPaths, hostHomeJoin(home, ".pi", "agent"))
}

// hostHomeJoin joins paths under the builder's host home using / separators.
func hostHomeJoin(home string, parts ...string) string {
	joined := home
	for _, p := range parts {
		joined = joined + "/" + p
	}
	return joined
}