package vmlua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript writes content to <tmp>/.config/homura/vm.lua and returns its path.
func writeScript(t *testing.T, tmp, content string) string {
	t.Helper()
	dir := filepath.Join(tmp, ".config", "homura")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, "vm.lua")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write vm.lua: %v", err)
	}
	return p
}

func loadScript(t *testing.T, script string, opts ...Option) *Result {
	t.Helper()
	tmp := t.TempDir()
	p := writeScript(t, tmp, script)
	opts = append([]Option{WithVersion(42), WithHostHome("/home/luna")}, opts...)
	res, err := Load(p, opts...)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return res
}

// AC.2 — aptInstall emits one sorted RUN per package, prefixed by FROM.
func TestDockerfileAptInstallGolden(t *testing.T) {
	res := loadScript(t, `homura.image(function(m) m:aptInstall("vim","tmux") end)`)
	got := string(res.Dockerfile())
	want := "FROM homura-vm-ubuntu-base:v42\n" +
		"RUN apt-get install -y --no-install-recommends tmux\n" +
		"RUN apt-get install -y --no-install-recommends vim\n"
	if got != want {
		t.Fatalf("Dockerfile():\n got %q\n want %q", got, want)
	}
}

// No homura.image block → Dockerfile() is nil.
func TestNoImageBlockNil(t *testing.T) {
	res := loadScript(t, `homura.config({cacheDir="/tmp/x"})`)
	if res.Dockerfile() != nil {
		t.Fatalf("Dockerfile() = %q, want nil", res.Dockerfile())
	}
}

// AC.3 — symlinkFromHost default registers read-only AllowPath + line.
func TestSymlinkFromHostDefaultRo(t *testing.T) {
	res := loadScript(t, `homura.image(function(m) m:symlinkFromHost("/h/a","/root/a") end)`)
	if !strings.Contains(string(res.Dockerfile()), "RUN ln -s /mnt/host/h/a /root/a") {
		t.Fatalf("missing ln -s line:\n%s", res.Dockerfile())
	}
	if !pathSpecPresent(res.AllowPaths, "/h/a", true) {
		t.Fatalf("AllowPaths = %v, want /h/a:ro", res.AllowPaths)
	}
}

// AC.3 rw — symlinkFromHost with {rw=true} registers read-write.
func TestSymlinkFromHostRw(t *testing.T) {
	res := loadScript(t, `homura.image(function(m) m:symlinkFromHost("/h/b","/root/b",{rw=true}) end)`)
	if !pathSpecPresent(res.AllowPaths, "/h/b", false) {
		t.Fatalf("AllowPaths = %v, want /h/b (rw)", res.AllowPaths)
	}
}

// AC.4 — symlinkFromHost with {noExpose=true} does not register AllowPaths.
func TestSymlinkFromHostNoExpose(t *testing.T) {
	res := loadScript(t, `homura.image(function(m) m:symlinkFromHost("/h/c","/root/c",{noExpose=true}) end)`)
	if !strings.Contains(string(res.Dockerfile()), "RUN ln -s /mnt/host/h/c /root/c") {
		t.Fatalf("missing ln -s line:\n%s", res.Dockerfile())
	}
	if pathSpecPresent(res.AllowPaths, "/h/c", true) || pathSpecPresent(res.AllowPaths, "/h/c", false) {
		t.Fatalf("AllowPaths = %v, want /h/c absent", res.AllowPaths)
	}
}

// config allowPaths + symlink-registered paths merge, deduped by normalized path.
func TestAllowPathsMerge(t *testing.T) {
	res := loadScript(t, `
homura.config({allowPaths={"/x/y:ro"}})
homura.image(function(m)
  m:symlinkFromHost("/w/z","/root/z",{rw=true})
  m:symlinkFromHost("/x/y","/root/y",{rw=true})
end)
`)
	var ro, rw bool
	for _, p := range res.AllowPaths {
		if p == "/x/y:ro" {
			ro = true
		} else if p == "/w/z" {
			rw = true
		}
	}
	if !ro || !rw {
		t.Fatalf("AllowPaths = %v, want /x/y:ro and /w/z", res.AllowPaths)
	}
}

// homura.config maps all fields.
func TestConfigFields(t *testing.T) {
	res := loadScript(t, `
homura.config({
  stateDir="/s/state",
  cacheDir="/c/cache",
  snapshot={"/a","/b"},
  allowPaths={"/p:ro"},
  maxSnapshots=3,
})
`)
	if res.StateDir != "/s/state" {
		t.Fatalf("StateDir = %q", res.StateDir)
	}
	if res.CacheDir != "/c/cache" {
		t.Fatalf("CacheDir = %q", res.CacheDir)
	}
	if len(res.Snapshot) != 2 || res.Snapshot[0] != "/a" || res.Snapshot[1] != "/b" {
		t.Fatalf("Snapshot = %v", res.Snapshot)
	}
	if res.MaxSnapshots == nil || *res.MaxSnapshots != 3 {
		t.Fatalf("MaxSnapshots = %v", res.MaxSnapshots)
	}
	if len(res.AllowPaths) != 1 || res.AllowPaths[0] != "/p:ro" {
		t.Fatalf("AllowPaths = %v", res.AllowPaths)
	}
}

// maxSnapshots absent → nil.
func TestConfigMaxSnapshotsAbsent(t *testing.T) {
	res := loadScript(t, `homura.config({})`)
	if res.MaxSnapshots != nil {
		t.Fatalf("MaxSnapshots = %v, want nil", res.MaxSnapshots)
	}
}

// pathSpecPresent reports whether spec (with ro/without) is in specs.
func pathSpecPresent(specs []string, path string, ro bool) bool {
	wantRW := path
	wantRO := path + ":ro"
	for _, s := range specs {
		if ro && s == wantRO {
			return true
		}
		if !ro && s == wantRW {
			return true
		}
	}
	return false
}