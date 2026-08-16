package vm

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStateDirDefault(t *testing.T) {
	t.Setenv("HOMURA_VM_STATE_DIR", "")
	base, defaulted := resolveStateDir("", "/cache/root", &VMConfig{})
	want := filepath.Join("/cache/root", "state")
	if base != want {
		t.Fatalf("resolveStateDir() = %q, want %q", base, want)
	}
	if !defaulted {
		t.Fatalf("resolveStateDir() defaulted = false, want true")
	}
}

func TestResolveStateDirEnv(t *testing.T) {
	t.Setenv("HOMURA_VM_STATE_DIR", "/env/state")
	base, defaulted := resolveStateDir("", "/cache/root", &VMConfig{})
	if base != "/env/state" {
		t.Fatalf("resolveStateDir() = %q, want %q", base, "/env/state")
	}
	if defaulted {
		t.Fatalf("resolveStateDir() defaulted = true, want false")
	}
}

func TestResolveStateDirConfig(t *testing.T) {
	t.Setenv("HOMURA_VM_STATE_DIR", "")
	base, defaulted := resolveStateDir("", "/cache/root", &VMConfig{StateDir: "/cfg/state"})
	if base != "/cfg/state" {
		t.Fatalf("resolveStateDir() = %q, want %q", base, "/cfg/state")
	}
	if defaulted {
		t.Fatalf("resolveStateDir() defaulted = true, want false")
	}
}

func TestResolveStateDirExplicit(t *testing.T) {
	t.Setenv("HOMURA_VM_STATE_DIR", "/env/state")
	base, defaulted := resolveStateDir("/explicit/state", "/cache/root", &VMConfig{StateDir: "/cfg/state"})
	if base != "/explicit/state" {
		t.Fatalf("resolveStateDir() = %q, want %q", base, "/explicit/state")
	}
	if defaulted {
		t.Fatalf("resolveStateDir() defaulted = true, want false")
	}
}

func TestCheckStateDirSpaceFail(t *testing.T) {
	dir := t.TempDir()
	// An impossibly large minimum always exceeds any real filesystem's free space.
	err := checkStateDirSpace(dir, 1<<62)
	if err == nil {
		t.Fatalf("checkStateDirSpace() = nil, want error")
	}
	if !strings.Contains(err.Error(), "configure") && !strings.Contains(err.Error(), "Configure") {
		t.Fatalf("checkStateDirSpace() error should direct user to configure stateDir, got: %v", err)
	}
}

func TestCheckStateDirSpacePass(t *testing.T) {
	dir := t.TempDir()
	err := checkStateDirSpace(dir, 1)
	if err != nil {
		t.Fatalf("checkStateDirSpace() = %v, want nil", err)
	}
}

func TestRootfsSizeBytes(t *testing.T) {
	n, err := rootfsSizeBytes()
	if err != nil {
		t.Fatalf("rootfsSizeBytes() error: %v", err)
	}
	want := uint64(50) * 1024 * 1024 * 1024
	if n != want {
		t.Fatalf("rootfsSizeBytes() = %d, want %d", n, want)
	}
}