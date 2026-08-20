package vmlua

import (
	"os"
	"path/filepath"
	"testing"
)

// AC.7 — when vm.lua is absent, Load returns an empty/default Result regardless
// of leftover vm.json / Dockerfile.custom files.
func TestLoadAbsentIgnoresOldFiles(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".config", "homura")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Old files present, no vm.lua.
	os.WriteFile(filepath.Join(dir, "vm.json"), []byte(`{"configVersion":1,"cacheDir":"/old/cache"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "Dockerfile.custom"), []byte(`FROM homura-vm-ubuntu-base:v0\n`), 0o644)

	res, err := Load(filepath.Join(dir, "vm.lua"), WithVersion(42), WithHostHome("/home/luna"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil empty Result")
	}
	if res.CacheDir != "" || res.StateDir != "" {
		t.Fatalf("expected defaults, got StateDir=%q CacheDir=%q", res.StateDir, res.CacheDir)
	}
	if len(res.AllowPaths) != 0 || len(res.Snapshot) != 0 || res.MaxSnapshots != nil {
		t.Fatalf("expected empty config, got %+v", res)
	}
	if res.Dockerfile() != nil {
		t.Fatalf("Dockerfile() should be nil, got %q", res.Dockerfile())
	}
}

// A malformed vm.lua returns a parse error (repurposed TestCacheDirBadVersion).
func TestLoadMalformedScriptErrors(t *testing.T) {
	tmp := t.TempDir()
	p := writeScript(t, tmp, `this is not ( lua`)
	if _, err := Load(p, WithVersion(42), WithHostHome("/home/luna")); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

// Missing file entirely is not an error.
func TestLoadMissingPathNoError(t *testing.T) {
	tmp := t.TempDir()
	res, err := Load(filepath.Join(tmp, "nope", "vm.lua"), WithVersion(42), WithHostHome("/home/luna"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil Result")
	}
}