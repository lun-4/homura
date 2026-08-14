package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// VMConfig represents the persistent VM configuration
type VMConfig struct {
	ConfigVersion int      `json:"configVersion"`
	AllowPaths    []string `json:"allowPaths"`    // Paths with optional :ro/:rw suffix
	Snapshot      []string `json:"snapshot"`      // Paths to snapshot before each VM start
	MaxSnapshots  *int     `json:"maxSnapshots"`  // Max snapshots to keep (default 7)
	StateDir      string   `json:"stateDir"`      // Base dir for per-VM state (ephemeral disk, sockets). Empty = system temp dir
	CacheDir      string   `json:"cacheDir"`      // Override for ~/.cache/homura root. Empty = default
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
// or "" to use the system temp dir
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

// CurrentConfigVersion is the current config schema version
const CurrentConfigVersion = 1

// LoadVMConfig loads the VM configuration from ~/.config/homura/vm.json
// Returns nil config (not an error) if the file doesn't exist
func LoadVMConfig() (*VMConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, ".config", "homura", "vm.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No config file, not an error
		}
		return nil, fmt.Errorf("failed to read vm.json: %w", err)
	}

	var config VMConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse vm.json: %w", err)
	}

	// Validate config version
	if config.ConfigVersion != CurrentConfigVersion {
		return nil, fmt.Errorf("vm.json has configVersion %d, but homura expects version %d\n"+
			"Please update your config file at %s",
			config.ConfigVersion, CurrentConfigVersion, configPath)
	}

	return &config, nil
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
