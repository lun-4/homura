package vm

import (
	"os"
	"path/filepath"
	"strings"
)

// CacheDir returns the root directory for homura's cache tree (VM images,
// SSH host keys, bin/, src/, snapshots, logs). It defaults to
// $HOME/.cache/homura, or the configured cacheDir from vm.json when present.
func CacheDir() (string, error) {
	cfg, err := LoadVMConfig()
	if err != nil {
		return "", err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if cfg != nil && cfg.CacheDir != "" {
		cacheDir := cfg.CacheDir
		// Expand a leading "~/" against the user's home directory.
		if strings.HasPrefix(cacheDir, "~/") {
			cacheDir = filepath.Join(homeDir, strings.TrimPrefix(cacheDir, "~/"))
		}
		abs, err := filepath.Abs(cacheDir)
		if err != nil {
			return "", err
		}
		return abs, nil
	}

	return filepath.Join(homeDir, ".cache", "homura"), nil
}