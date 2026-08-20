package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC.10 — customImageDecision derives hasCustom/content/imageName from a config
// with a custom Dockerfile, and empty when there's none.
func TestCustomImageDecision(t *testing.T) {
	// No custom Dockerfile → no custom layer.
	if hasCustom, content, name := customImageDecision(nil, "baseid"); hasCustom || content != nil || name != "" {
		t.Fatalf("nil config: got hasCustom=%v content=%q name=%q", hasCustom, content, name)
	}

	cfg := &VMConfig{}
	if hasCustom, content, name := customImageDecision(cfg, "baseid"); hasCustom || content != nil || name != "" {
		t.Fatalf("config without dockerfile: got hasCustom=%v name=%q", hasCustom, name)
	}

	df := []byte("FROM homura-vm-ubuntu-base:v42\nRUN apt-get update\n")
	cfg.customDockerfile = df

	hasCustom, content, name := customImageDecision(cfg, "imagid")
	if !hasCustom {
		t.Fatal("expected hasCustom=true")
	}
	if string(content) != string(df) {
		t.Fatalf("content = %q, want %q", content, df)
	}
	wantPrefix := "homura-vm-ubuntu-custom:"
	if !strings.HasPrefix(name, wantPrefix) || len(name) == len(wantPrefix) {
		t.Fatalf("imageName = %q, want %q<hash>", name, wantPrefix)
	}

	// hasCustom=false / empty when CustomDockerfile is nil (via LoadVMConfig
	// without a vm.lua).
	absent := &VMConfig{customDockerfile: nil}
	if hasCustom, content, name := customImageDecision(absent, "imagid"); hasCustom || content != nil || name != "" {
		t.Fatalf("absent dockerfile: got hasCustom=%v", hasCustom)
	}
}

// AC.9 — a recipe-body change (same FROM) and a version bump (different FROM)
// both yield a different custom image name/hash.
func TestCustomImageDecisionHashChanges(t *testing.T) {
	cfgA := &VMConfig{}
	cfgA.customDockerfile = []byte("FROM homura-vm-ubuntu-base:v42\nRUN apt-get install vim\n")
	cfgB := &VMConfig{}
	cfgB.customDockerfile = []byte("FROM homura-vm-ubuntu-base:v42\nRUN apt-get install tmux\n") // recipe-body change
	cfgV := &VMConfig{}
	cfgV.customDockerfile = []byte("FROM homura-vm-ubuntu-base:v43\nRUN apt-get install vim\n") // version bump

	_, _, a := customImageDecision(cfgA, "baseid")
	_, _, b := customImageDecision(cfgB, "baseid")
	_, _, v := customImageDecision(cfgV, "baseid")

	if a == b {
		t.Fatalf("recipe-body change produced same image name %q", a)
	}
	if a == v {
		t.Fatalf("version bump produced same image name %q", a)
	}
	if b == v {
		t.Fatalf("distinct changes produced same name %q", b)
	}
}

// AC.7 — with vm.lua absent but vm.json / Dockerfile.custom present, LoadVMConfig
// returns nil (defaults) and CustomDockerfile() is nil; CacheDir falls back to
// the default.
func TestLoadVMConfigIgnoresOldFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := filepath.Join(tmp, ".config", "homura")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "vm.json"), []byte(`{"configVersion":1,"cacheDir":"/old/cache"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "Dockerfile.custom"), []byte("FROM homura-vm-ubuntu-base:v0\n"), 0o644)

	cfg, err := LoadVMConfig()
	if err != nil {
		t.Fatalf("LoadVMConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config (defaults), got %+v", cfg)
	}
	if cacheDir, err := CacheDir(); err != nil || cacheDir != filepath.Join(tmp, ".cache", "homura") {
		t.Fatalf("CacheDir = %q, err=%v (default expected)", cacheDir, err)
	}
}

// AC.1 — a vm.lua with homura.config({cacheDir=...}) drives GetCacheDir().
func TestLoadVMConfigFromLua(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	writeVMConfig(t, tmp, `homura.config({cacheDir="/lua/cache", maxSnapshots=5})`)

	cfg, err := LoadVMConfig()
	if err != nil {
		t.Fatalf("LoadVMConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.GetCacheDir() != "/lua/cache" {
		t.Fatalf("GetCacheDir = %q", cfg.GetCacheDir())
	}
	if cfg.GetMaxSnapshots() != 5 {
		t.Fatalf("GetMaxSnapshots = %d", cfg.GetMaxSnapshots())
	}
	if cfg.CustomDockerfile() != nil {
		t.Fatalf("CustomDockerfile should be nil without homura.image, got %q", cfg.CustomDockerfile())
	}
}

// LoadVMConfig maps symlink-registered paths (from homura.image) into
// AllowPaths and dedupes against auto-exposed claude paths.
func TestLoadVMConfigMergesAllowPaths(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	writeVMConfig(t, tmp, `
homura.config({allowPaths={"/data:ro"}})
homura.image(function(m)
  m:symlinkFromHost("/data","/root/data",{rw=true})
end)
`)
	cfg, err := LoadVMConfig()
	if err != nil {
		t.Fatalf("LoadVMConfig: %v", err)
	}
	// /data appears once (deduped by normalized path; first occurrence ro wins).
	var count int
	for _, p := range cfg.AllowPaths {
		if strings.TrimSuffix(p, ":ro") == "/data" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("AllowPaths = %v, want exactly one /data entry", cfg.AllowPaths)
	}
	if cfg.CustomDockerfile() == nil {
		t.Fatal("CustomDockerfile should be non-nil (image block present)")
	}
}