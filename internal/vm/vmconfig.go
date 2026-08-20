package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lun-4/homura/internal/vmlua"
)

// ShareMode represents the filesystem sharing mode for VMs
type ShareMode string

const (
	ShareMode9P      ShareMode = "9p"
	ShareModeVirtioFS ShareMode = "virtiofs"
)

// ParseShareMode validates and returns a ShareMode from a string
func ParseShareMode(s string) (ShareMode, error) {
	switch strings.ToLower(s) {
	case "9p":
		return ShareMode9P, nil
	case "virtiofs", "":
		return ShareModeVirtioFS, nil
	default:
		return "", fmt.Errorf("invalid share mode %q: must be '9p' or 'virtiofs'", s)
	}
}

// VMConfig represents the persistent VM configuration. It is populated from
// the sandboxed vm.lua script (via vmlua) rather than vm.json as of the Lua
// cutover. The JSON tags are retained only as documentation of the old schema;
// no file reading/validation of vm.json occurs.
type VMConfig struct {
	ConfigVersion int      `json:"configVersion"`
	AllowPaths    []string `json:"allowPaths"`    // Paths with optional :ro/:rw suffix
	Snapshot      []string `json:"snapshot"`      // Paths to snapshot before each VM start
	MaxSnapshots  *int     `json:"maxSnapshots"`  // Max snapshots to keep (default 7)
	StateDir      string   `json:"stateDir"`      // Base dir for per-VM state (ephemeral disk, sockets). Empty = <cacheRoot>/state
	CacheDir      string   `json:"cacheDir"`      // Override for ~/.cache/homura root. Empty = default

	// customDockerfile holds the generated custom Dockerfile content from the
	// vm.lua homura.image block. Nil means no custom image layer.
	customDockerfile []byte
}

// CustomDockerfile returns the generated custom Dockerfile content (nil when
// the vm.lua had no homura.image block, or when no vm.lua is configured).
func (c *VMConfig) CustomDockerfile() []byte {
	if c == nil {
		return nil
	}
	return c.customDockerfile
}

// DefaultMaxSnapshots is the default number of snapshots to keep
const DefaultMaxSnapshots = 7

// GetMaxSnapshots returns the max snapshots setting, defaulting to 7
func (c *VMConfig) GetMaxSnapshots() int {
	if c == nil || c.MaxSnapshots == nil {
		return DefaultMaxSnapshots
	}
	return *c.MaxSnapshots
}

// GetSnapshotPaths returns the list of paths to snapshot
func (c *VMConfig) GetSnapshotPaths() []string {
	if c == nil {
		return nil
	}
	return c.Snapshot
}

// GetStateDir returns the configured base directory for per-VM state,
// or "" to use the default <cacheRoot>/state
func (c *VMConfig) GetStateDir() string {
	if c == nil {
		return ""
	}
	return c.StateDir
}

// GetCacheDir returns the configured cache root override, or "" to use the
// default ~/.cache/homura
func (c *VMConfig) GetCacheDir() string {
	if c == nil {
		return ""
	}
	return c.CacheDir
}

// PathSpec represents a parsed path specification
type PathSpec struct {
	Path     string
	ReadOnly bool
}

// CurrentConfigVersion is the old vm.json schema version, retained for
// documentation only. vm.lua has no configVersion field — homura's own
// VMImplementationVersion gates compatibility.
const CurrentConfigVersion = 1

// LoadVMConfig loads the VM configuration from ~/.config/homura/vm.lua.
// Returns nil config (not an error) if vm.lua doesn't exist. When vm.lua is
// absent, any leftover vm.json / Dockerfile.custom files are ignored entirely
// (clean manual cutover — no fallback).
func LoadVMConfig() (*VMConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, ".config", "homura", "vm.lua")

	// No vm.lua → nil config (defaults), regardless of leftover vm.json /
	// Dockerfile.custom.
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to stat vm.lua: %w", err)
	}

	res, err := vmlua.Load(configPath, vmlua.WithVersion(VMImplementationVersion), vmlua.WithHostHome(homeDir))
	if err != nil {
		return nil, err
	}

	if res == nil {
		return nil, nil
	}

	cfg := &VMConfig{}
	cfg.AllowPaths = dedupeAllowPaths(res.AllowPaths, homeDir)
	cfg.Snapshot = res.Snapshot
	cfg.MaxSnapshots = res.MaxSnapshots
	cfg.StateDir = res.StateDir
	cfg.CacheDir = res.CacheDir
	cfg.customDockerfile = res.Dockerfile()
	return cfg, nil
}

// autoExposePaths are the host paths homura exposes automatically (in
// vm.NewVM), so allowPaths entries for them are redundant. Deduped out of the
// config so they aren't re-added to the 9passthrough argument list.
func autoExposePaths(homeDir string) []string {
	return []string{
		filepath.Join(homeDir, ".claude.json"),
		filepath.Join(homeDir, ".claude"),
		filepath.Join(homeDir, ".claude.lock"),
		filepath.Join(homeDir, ".config", "homura", "CLAUDE.md"),
	}
}

// dedupeAllowPaths dedupes the Lua-derived allowPaths by normalized path (a
// trailing :ro/:rw suffix is ignored for matching) and drops any that collide
// with homura's auto-exposed paths. First occurrence wins.
func dedupeAllowPaths(paths []string, homeDir string) []string {
	seen := make(map[string]bool, len(paths))
	// Pre-seed with auto-exposed paths so matching entries get dropped.
	for _, p := range autoExposePaths(homeDir) {
		seen[filepath.Clean(p)] = true
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		base := ParsePathSpec(p).Path
		for _, a := range autoExposePaths(homeDir) {
			if filepath.Clean(base) == filepath.Clean(a) {
				goto skip
			}
		}
		if seen[filepath.Clean(base)] {
			goto skip
		}
		seen[filepath.Clean(base)] = true
		out = append(out, p)
	skip:
	}
	return out
}

// ParsePathSpec parses a path specification like "/path:ro" or "/path:rw" or "/path"
// Default is read-write if no suffix is provided
func ParsePathSpec(spec string) PathSpec {
	if strings.HasSuffix(spec, ":ro") {
		return PathSpec{
			Path:     strings.TrimSuffix(spec, ":ro"),
			ReadOnly: true,
		}
	}
	if strings.HasSuffix(spec, ":rw") {
		return PathSpec{
			Path:     strings.TrimSuffix(spec, ":rw"),
			ReadOnly: false,
		}
	}
	// Default to read-write
	return PathSpec{
		Path:     spec,
		ReadOnly: false,
	}
}

// GetAllowPaths returns the list of parsed path specifications from the config
func (c *VMConfig) GetAllowPaths() []PathSpec {
	if c == nil {
		return nil
	}

	specs := make([]PathSpec, 0, len(c.AllowPaths))
	for _, p := range c.AllowPaths {
		specs = append(specs, ParsePathSpec(p))
	}
	return specs
}

// FormatPathArg formats a PathSpec as a command-line argument for 9passthrough
// Format: "path" for rw, "path:ro" for read-only
func (p PathSpec) FormatPathArg() string {
	if p.ReadOnly {
		return p.Path + ":ro"
	}
	return p.Path
}
