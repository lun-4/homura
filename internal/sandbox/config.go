package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
)

const (
	// EnvReexec is set to indicate we're inside the sandboxed child process
	EnvReexec = "HOMURA_SANDBOX_REEXEC"
	// EnvConfig contains the JSON-encoded SandboxConfig
	EnvConfig = "HOMURA_SANDBOX_CONFIG"
	// ReexecInit is the value of EnvReexec when we're in the init phase
	ReexecInit = "init"
)

// SandboxConfig holds the configuration passed from parent to child process
type SandboxConfig struct {
	WorktreePath   string `json:"worktree_path"`
	BranchName     string `json:"branch_name"`
	Shell          string `json:"shell"`
	RepoRoot       string `json:"repo_root"`
	FuseMountPoint string `json:"fuse_mount_point"`
}

// Encode serializes the config to JSON for passing via environment variable
func (c *SandboxConfig) Encode() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("failed to encode sandbox config: %w", err)
	}
	return string(data), nil
}

// DecodeConfig deserializes a config from the environment variable
func DecodeConfig() (*SandboxConfig, error) {
	data := os.Getenv(EnvConfig)
	if data == "" {
		return nil, fmt.Errorf("sandbox config not found in environment")
	}

	var cfg SandboxConfig
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("failed to decode sandbox config: %w", err)
	}
	return &cfg, nil
}

// IsReexecChild returns true if we're running inside the sandboxed child process
func IsReexecChild() bool {
	return os.Getenv(EnvReexec) == ReexecInit
}
