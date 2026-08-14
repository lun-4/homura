package vm

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SnapshotDir returns the path to the snapshots directory
func SnapshotDir() (string, error) {
	root, err := CacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get cache directory: %w", err)
	}
	return filepath.Join(root, "snapshots"), nil
}

// hasTodaySnapshot checks if a snapshot already exists for today
func hasTodaySnapshot(snapshotDir string) (bool, error) {
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to read snapshot directory: %w", err)
	}

	todayPrefix := time.Now().Format("20060102")
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tar") {
			if strings.HasPrefix(e.Name(), todayPrefix) {
				return true, nil
			}
		}
	}
	return false, nil
}

// CreateSnapshot creates a tarball of the specified paths with absolute paths preserved.
// It also cleans up old snapshots beyond maxSnapshots.
// Snapshots are taken at most once per day - if a snapshot already exists for today,
// this function returns early without creating a new one.
func CreateSnapshot(paths []string, maxSnapshots int) error {
	if len(paths) == 0 {
		return nil
	}

	snapshotDir, err := SnapshotDir()
	if err != nil {
		return err
	}

	// Check if we already have a snapshot for today
	hasToday, err := hasTodaySnapshot(snapshotDir)
	if err != nil {
		slog.Warn("Failed to check for today's snapshot", "error", err)
		// Continue anyway - better to potentially create a duplicate than skip
	} else if hasToday {
		slog.Info("Snapshot already exists for today, skipping")
		return nil
	}

	// Ensure snapshot directory exists
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		return fmt.Errorf("failed to create snapshot directory: %w", err)
	}

	// Validate paths exist
	validPaths := make([]string, 0, len(paths))
	for _, p := range paths {
		// Expand ~ if present
		if strings.HasPrefix(p, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				slog.Warn("Failed to expand ~ in path", "path", p, "error", err)
				continue
			}
			p = filepath.Join(homeDir, p[2:])
		}

		// Make path absolute
		absPath, err := filepath.Abs(p)
		if err != nil {
			slog.Warn("Failed to get absolute path", "path", p, "error", err)
			continue
		}

		// Check if path exists
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			slog.Warn("Snapshot path does not exist, skipping", "path", absPath)
			continue
		} else if err != nil {
			slog.Warn("Failed to stat path", "path", absPath, "error", err)
			continue
		}

		validPaths = append(validPaths, absPath)
	}

	if len(validPaths) == 0 {
		slog.Info("No valid paths to snapshot")
		return nil
	}

	// Generate timestamp filename
	timestamp := time.Now().Format("20060102-150405")
	snapshotFile := filepath.Join(snapshotDir, fmt.Sprintf("%s.tar", timestamp))

	slog.Info("Creating snapshot", "file", snapshotFile, "paths", validPaths)

	// Build tar command with -P to preserve absolute paths
	// Using -P flag makes tar store absolute paths as-is
	args := []string{"-cPf", snapshotFile}
	args = append(args, validPaths...)

	cmd := exec.Command("tar", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create snapshot tarball: %w\nOutput: %s", err, string(output))
	}

	slog.Info("Snapshot created successfully", "file", snapshotFile)

	// Clean up old snapshots
	if err := cleanupOldSnapshots(snapshotDir, maxSnapshots); err != nil {
		slog.Warn("Failed to cleanup old snapshots", "error", err)
		// Don't return error, snapshot was created successfully
	}

	return nil
}

// cleanupOldSnapshots removes old snapshot files beyond the max limit
func cleanupOldSnapshots(snapshotDir string, maxSnapshots int) error {
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		return fmt.Errorf("failed to read snapshot directory: %w", err)
	}

	// Filter to only .tar files
	var snapshots []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tar") {
			snapshots = append(snapshots, e)
		}
	}

	// If we're within the limit, nothing to do
	if len(snapshots) <= maxSnapshots {
		return nil
	}

	// Sort by name (which is timestamp, so oldest first)
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Name() < snapshots[j].Name()
	})

	// Remove oldest snapshots
	toRemove := len(snapshots) - maxSnapshots
	for i := 0; i < toRemove; i++ {
		oldPath := filepath.Join(snapshotDir, snapshots[i].Name())
		slog.Info("Removing old snapshot", "file", oldPath)
		if err := os.Remove(oldPath); err != nil {
			slog.Warn("Failed to remove old snapshot", "file", oldPath, "error", err)
		}
	}

	return nil
}
