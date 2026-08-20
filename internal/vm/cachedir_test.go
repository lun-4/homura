package vm

import (
	"os"
	"path/filepath"
	"testing"
)

func writeVMConfig(t *testing.T, tmp string, content string) {
	t.Helper()
	cfgDir := filepath.Join(tmp, ".config", "homura")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "vm.lua"), []byte(content), 0644); err != nil {
		t.Fatalf("write vm.lua: %v", err)
	}
}

func TestCacheDirDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	got, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir() error: %v", err)
	}
	want := filepath.Join(tmp, ".cache", "homura")
	if got != want {
		t.Fatalf("CacheDir() = %q, want %q", got, want)
	}
}

func TestCacheDirAbsolute(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	writeVMConfig(t, tmp, `homura.config({cacheDir="/var/cache/homura-custom"})`)

	got, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir() error: %v", err)
	}
	want := "/var/cache/homura-custom"
	if got != want {
		t.Fatalf("CacheDir() = %q, want %q", got, want)
	}
}

func TestCacheDirTilde(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	writeVMConfig(t, tmp, `homura.config({cacheDir="~/custom-cache"})`)

	got, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir() error: %v", err)
	}
	want := filepath.Join(tmp, "custom-cache")
	if got != want {
		t.Fatalf("CacheDir() = %q, want %q", got, want)
	}
}

func TestCacheDirRelative(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	writeVMConfig(t, tmp, `homura.config({cacheDir="relative/cache"})`)

	got, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir() error: %v", err)
	}
	want, _ := filepath.Abs("relative/cache")
	if got != want {
		t.Fatalf("CacheDir() = %q, want %q", got, want)
	}
}

func TestCacheDirMalformedVmLua(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// A malformed vm.lua returns a parse error instead of silently defaulting.
	writeVMConfig(t, tmp, `this is not ( lua`)

	if _, err := CacheDir(); err == nil {
		t.Fatal("CacheDir() expected error for malformed vm.lua, got nil")
	}
}