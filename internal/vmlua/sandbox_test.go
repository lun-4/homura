package vmlua

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// forbiddenGlobals is the list of globals that must be nil after sandbox setup
// (AC.6).
var forbiddenGlobals = []string{
	"os", "io", "require", "dofile", "loadfile", "loadstring", "load", "debug", "package",
}

// Probe the sandboxed state directly: open the exact set of libs Load uses and
// assert each forbidden global is nil.
func TestSandboxForbiddenGlobalsNil(t *testing.T) {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer L.Close()
	lua.OpenBase(L)
	lua.OpenTable(L)
	lua.OpenString(L)
	for _, g := range forbiddenBase {
		L.SetGlobal(g, lua.LNil)
	}
	for _, g := range forbiddenGlobals {
		if L.GetGlobal(g) != lua.LNil {
			t.Fatalf("forbidden global %q is not nil", g)
		}
	}
}

// AC.6 — a script that tries to use a forbidden global must error, not execute.
func TestSandboxScriptUsingForbiddenErrs(t *testing.T) {
	scripts := []string{
		`os.exit(1)`,
		`io.open("/etc/passwd")`,
		`dofile("/etc/passwd")`,
		`loadfile("/etc/hostname")`,
		`loadstring("os.exit(1)")`,
	}
	for _, src := range scripts {
		tmp := t.TempDir()
		p := writeScript(t, tmp, src)
		if _, err := Load(p, WithVersion(42), WithHostHome("/home/luna")); err == nil {
			t.Fatalf("Load(%q) expected error, got nil", src)
		}
	}
}

// Safe base functions still work (arithmetic, string, table).
func TestSandboxSafeFunctions(t *testing.T) {
	res, err := loadScriptSafe(t,
		`local x = tostring(2 + 2)
		 assert(x == "4")
		 local t = {1, 2, 3}
		 assert(#t == 3)
		 homura.config({cacheDir="/tmp/x"})`,
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.CacheDir != "/tmp/x" {
		t.Fatalf("CacheDir = %q", res.CacheDir)
	}
}

func loadScriptSafe(t *testing.T, script string) (*Result, error) {
	tmp := t.TempDir()
	p := writeScript(t, tmp, script)
	return Load(p, WithVersion(42), WithHostHome("/home/luna"))
}