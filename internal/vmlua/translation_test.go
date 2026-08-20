package vmlua

import (
	"strings"
	"testing"
)

// AC.8 — the full translated user config (maki allowPaths + all tool recipes)
// generates a Dockerfile with the expected RUN/ln -s lines and a merged
// AllowPaths with the correct ro/rw modes. Does not build/boot a VM.
func TestTranslationGolden(t *testing.T) {
	script := `
homura.config({
  allowPaths={
    "/home/luna/.config/maki:ro",
    "/home/luna/.local/share/maki",
    "/home/luna/.cache/maki",
  },
})
homura.image(function(m)
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
	df := string(res.Dockerfile())

	// Required recipe lines are all present.
	need := []string{
		"FROM homura-vm-ubuntu-base:v42",
		"mkdir -p /root/helix && curl -fsSL https://github.com/helix-editor/helix/releases/download/25.07.1/helix-25.07.1-x86_64-linux.tar.xz",
		"RUN go install golang.org/dl/go1.24.0@latest && go1.24.0 download",
		"RUN mix local.hex --force && mix local.rebar --force",
		"RUN ~/.local/bin/claude update 2.1.233 && ~/.local/bin/claude --version | grep -qF \"2.1.233\"",
		"RUN curl -fsSL https://sh.rustup.rs -o /tmp/rustup-init",
		"RUN curl -fsS https://get.polytoken.dev | bash",
		"RUN ln -s /mnt/host/home/luna/.local/share/polytoken /root/.local/share/polytoken",
		"RUN ln -s /mnt/host/home/luna/.pi /root/.pi",
		"RUN curl -fsSL https://pi.dev/install.sh | sh",
	}
	for _, s := range need {
		if !strings.Contains(df, s) {
			t.Fatalf("Dockerfile missing %q\n---\n%s", s, df)
		}
	}

	// AllowPaths: maki config ro; maki state/cache rw; polytoken/pi paths rw.
	expectRO := map[string]bool{"/home/luna/.config/maki": true}
	expectRW := map[string]bool{
		"/home/luna/.local/share/maki":        true,
		"/home/luna/.cache/maki":              true,
		"/home/luna/.cache/polytoken":         true,
		"/home/luna/.config/polytoken":        true,
		"/home/luna/.local/share/polytoken":   true,
		"/home/luna/.pi/agent":                true,
	}
	var gotRO, gotRW []string
	for _, p := range res.AllowPaths {
		if strings.HasSuffix(p, ":ro") {
			gotRO = append(gotRO, strings.TrimSuffix(p, ":ro"))
		} else {
			gotRW = append(gotRW, p)
		}
	}
	for _, p := range gotRO {
		if !expectRO[p] {
			t.Fatalf("unexpected ro path %q in %v", p, res.AllowPaths)
		}
	}
	for _, p := range gotRW {
		if !expectRW[p] {
			t.Fatalf("unexpected rw path %q in %v", p, res.AllowPaths)
		}
	}
	if len(gotRO) != len(expectRO) || len(gotRW) != len(expectRW) {
		t.Fatalf("path counts wrong: ro=%v rw=%v (all=%v)", gotRO, gotRW, res.AllowPaths)
	}
}