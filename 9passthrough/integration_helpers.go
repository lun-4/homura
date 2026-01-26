package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// createTempFileTree creates a test directory with files and returns the root path.
// The files map uses relative paths as keys and file content as values.
// Directory paths should end with "/" to create empty directories.
func createTempFileTree(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for path, content := range files {
		fullPath := filepath.Join(dir, path)

		// If path ends with /, it's a directory
		if len(path) > 0 && path[len(path)-1] == '/' {
			if err := os.MkdirAll(fullPath, 0755); err != nil {
				t.Fatalf("Failed to create directory %s: %v", fullPath, err)
			}
			continue
		}

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("Failed to create parent directories for %s: %v", fullPath, err)
		}

		// Create file with content
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create file %s: %v", fullPath, err)
		}
	}
	return dir
}

// assertFileContent reads a file from the host filesystem and verifies its content.
func assertFileContent(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read file %s: %v", path, err)
	}
	if !bytes.Equal(actual, expected) {
		t.Errorf("Content mismatch in %s: got %q, want %q", path, actual, expected)
	}
}

// assertFileExists verifies a file exists on the host filesystem.
func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("File %s does not exist: %v", path, err)
	}
}

// assertFileNotExists verifies a file does not exist on the host filesystem.
func assertFileNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("File %s should not exist but it does", path)
	}
}

// assertSyscallError verifies an error is a specific syscall error.
// For permission errors, it accepts both EPERM and EACCES as equivalent.
func assertSyscallError(t *testing.T, err error, expected syscall.Errno) {
	t.Helper()
	if err == nil {
		t.Fatalf("Expected error %v, got nil", expected)
	}

	// Accept both EPERM and EACCES as permission errors
	if expected == syscall.EPERM || expected == syscall.EACCES {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return
		}
		// Also check error strings (for wrapped errors)
		errStr := err.Error()
		if errStr == "permission denied" || errStr == "operation not permitted" {
			return
		}
		t.Errorf("Expected permission error (EPERM or EACCES), got %v", err)
		return
	}

	// Check both with errors.Is and by comparing error strings
	// (some wrapped errors don't work with errors.Is)
	if errors.Is(err, expected) {
		return
	}

	// Fallback: compare error strings
	if err.Error() == expected.Error() {
		return
	}

	t.Errorf("Expected error %v, got %v", expected, err)
}

// skipIfNotRoot skips the test if not running as root (UID 0).
func skipIfNotRoot(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges")
	}
}

// skipIfNoXattrSupport checks if the filesystem supports extended attributes.
// It creates a temporary file and tries to set a user xattr.
func skipIfNoXattrSupport(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Try to get a user xattr (doesn't need to exist)
	_, err := unix.Getxattr(testFile, "user.test", nil)
	if err != nil && errors.Is(err, syscall.ENOTSUP) {
		t.Skip("Filesystem doesn't support extended attributes")
	}
}

// assertFileMode verifies a file has the expected permission mode.
func assertFileMode(t *testing.T, path string, expectedMode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat file %s: %v", path, err)
	}
	actualMode := info.Mode() & os.ModePerm
	if actualMode != expectedMode {
		t.Errorf("Mode mismatch for %s: got %o, want %o", path, actualMode, expectedMode)
	}
}

// assertFileSize verifies a file has the expected size.
func assertFileSize(t *testing.T, path string, expectedSize int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat file %s: %v", path, err)
	}
	if info.Size() != expectedSize {
		t.Errorf("Size mismatch for %s: got %d, want %d", path, info.Size(), expectedSize)
	}
}

// createLargeFile creates a file with the specified size filled with a repeating pattern.
func createLargeFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Failed to create large file: %v", err)
	}
	defer f.Close()

	// Write in 1MB chunks
	pattern := bytes.Repeat([]byte("0123456789abcdef"), 64*1024) // 1MB pattern
	written := int64(0)
	for written < size {
		toWrite := int64(len(pattern))
		if written+toWrite > size {
			toWrite = size - written
		}
		n, err := f.Write(pattern[:toWrite])
		if err != nil {
			t.Fatalf("Failed to write to large file: %v", err)
		}
		written += int64(n)
	}
}
