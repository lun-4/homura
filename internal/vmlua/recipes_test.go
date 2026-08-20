package vmlua

import (
	"strings"
	"testing"
)

// AC.5 — every core recipe emits a stable, version/pinned set of Dockerfile
// lines. This golden test fails on any emitted-line change.
func TestRecipesGolden(t *testing.T) {
	script := `
homura.image(function(m)
  m:addDockerUbuntuRepo()
  m:installHelix("25.07.1")
  m:installGo("1.24.0")
  m:installElixir()
  m:installClaude("2.1.233")
  m:installRust()
  m:installPolytoken()
  m:installPi()
end)
`
	res := loadScript(t, script)

	want := "FROM homura-vm-ubuntu-base:v42\n" +
		"RUN curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /usr/share/keyrings/docker.asc && echo 'deb [signed-by=/usr/share/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable' > /etc/apt/sources.list.d/docker.list && apt-get update\n" +
		"RUN mkdir -p /root/helix && curl -fsSL https://github.com/helix-editor/helix/releases/download/25.07.1/helix-25.07.1-x86_64-linux.tar.xz -o /tmp/helix.tar.xz && tar -xf /tmp/helix.tar.xz -C /root/helix --strip-components=1 && ln -sf /root/helix/hx /usr/local/bin/hx && rm /tmp/helix.tar.xz && hx --version | grep -qF \"25.07.1\"\n" +
		"RUN go install golang.org/dl/go1.24.0@latest && go1.24.0 download\n" +
		"RUN mix local.hex --force && mix local.rebar --force\n" +
		"RUN ~/.local/bin/claude update 2.1.233 && ~/.local/bin/claude --version | grep -qF \"2.1.233\"\n" +
		"RUN curl -fsSL https://sh.rustup.rs -o /tmp/rustup-init && sh /tmp/rustup-init -y --default-toolchain stable && rm /tmp/rustup-init\n" +
		"RUN curl -fsS https://get.polytoken.dev | bash\n" +
		"RUN mkdir -p /root/.local/share\n" +
		"RUN ln -s /mnt/host/home/luna/.cache/polytoken /root/.cache/polytoken\n" +
		"RUN ln -s /mnt/host/home/luna/.config/polytoken /root/.config/polytoken\n" +
		"RUN ln -s /mnt/host/home/luna/.local/share/polytoken /root/.local/share/polytoken\n" +
		"RUN apt-get install -y --no-install-recommends fd-find\n" +
		"RUN apt-get install -y --no-install-recommends nodejs\n" +
		"RUN apt-get install -y --no-install-recommends npm\n" +
		"RUN node --version && npm --version\n" +
		"RUN curl -fsSL https://pi.dev/install.sh | sh\n" +
		"RUN ln -s /mnt/host/home/luna/.pi /root/.pi\n"

	got := string(res.Dockerfile())
	if got != want {
		t.Fatalf("recipes Dockerfile mismatch.\n got:\n%s\n want:\n%s", got, want)
	}

	// Symlink-registered paths from polytoken + pi (all rw; pi exposes only
	// .pi/agent, and we assert the whole ~/.pi is NOT auto-registered).
	rwPaths := map[string]bool{
		"/home/luna/.cache/polytoken":         false,
		"/home/luna/.config/polytoken":        false,
		"/home/luna/.local/share/polytoken":   false,
		"/home/luna/.pi/agent":                false,
	}
	for _, p := range res.AllowPaths {
		if p == "/home/luna/.pi" {
			t.Fatalf("installPi must not auto-register the whole ~/.pi, got %v", res.AllowPaths)
		}
		delete(rwPaths, p) // rw entries have no suffix
	}
	if len(rwPaths) != 0 {
		t.Fatalf("missing rw AllowPaths in %v", rwPaths)
	}
}

// installElixir with opts toggles hex/rebar.
func TestInstallElixirOpts(t *testing.T) {
	res := loadScript(t, `homura.image(function(m) m:installElixir({hex=false}) end)`)
	df := string(res.Dockerfile())
	if strings.Contains(df, "mix local.hex") {
		t.Fatalf("expected hex disabled, got:\n%s", df)
	}
	if !strings.Contains(df, "RUN mix local.rebar --force") {
		t.Fatalf("expected rebar enabled, got:\n%s", df)
	}
}

// installGo with downloadViaSystemGo uses /usr/bin/go.
func TestInstallGoViaSystem(t *testing.T) {
	res := loadScript(t, `homura.image(function(m) m:installGo("1.24.0",{downloadViaSystemGo=true}) end)`)
	df := string(res.Dockerfile())
	if !strings.Contains(df, "RUN /usr/bin/go install golang.org/dl/go1.24.0@latest && go1.24.0 download") {
		t.Fatalf("unexpected line:\n%s", df)
	}
}