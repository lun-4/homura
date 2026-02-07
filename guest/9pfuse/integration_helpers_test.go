package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// testServer represents a running 9passthrough server for testing
type testServer struct {
	cmd     *exec.Cmd
	addr    string
	cancel  context.CancelFunc
	tempDir string
}

// startTestServer builds and starts a 9passthrough server with the given exposed paths.
// Returns the server address and a cleanup function.
func startTestServer(t *testing.T, exposedPaths []string) (addr string, cleanup func()) {
	t.Helper()

	// Find the 9passthrough directory relative to this test
	// We're in guest/9pfuse, so 9passthrough is ../../9passthrough
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// Navigate to find 9passthrough
	passthroughDir := filepath.Join(cwd, "..", "..", "9passthrough")
	if _, err := os.Stat(passthroughDir); os.IsNotExist(err) {
		// Try relative to the module root
		passthroughDir = filepath.Join(cwd, "../../9passthrough")
	}

	// Absolute path
	passthroughDir, err = filepath.Abs(passthroughDir)
	if err != nil {
		t.Fatalf("Failed to get absolute path for 9passthrough: %v", err)
	}

	// Build 9passthrough binary in a temp directory
	tmpBin := filepath.Join(t.TempDir(), "9passthrough")
	buildCmd := exec.Command("go", "build", "-o", tmpBin, ".")
	buildCmd.Dir = passthroughDir
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build 9passthrough: %v\n%s", err, output)
	}

	// Create context for the server
	ctx, cancel := context.WithCancel(context.Background())

	// Start the server with exposed paths
	args := make([]string, len(exposedPaths))
	copy(args, exposedPaths)

	cmd := exec.CommandContext(ctx, tmpBin, args...)
	cmd.Env = os.Environ()

	// Capture stderr to get the server address
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		t.Fatalf("Failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("Failed to start 9passthrough: %v", err)
	}

	// Parse server output to get the port
	// The server prints: "9p listening on: 0.0.0.0:<port>"
	portRegex := regexp.MustCompile(`9p listening on: [^:]+:(\d+)`)
	scanner := bufio.NewScanner(stderr)

	addrChan := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if matches := portRegex.FindStringSubmatch(line); len(matches) > 1 {
				addrChan <- "127.0.0.1:" + matches[1]
				return
			}
		}
	}()

	// Wait for server to start (with timeout)
	select {
	case addr = <-addrChan:
		// Server started
	case <-time.After(10 * time.Second):
		cancel()
		cmd.Wait()
		t.Fatalf("Timeout waiting for 9passthrough server to start")
	}

	cleanup = func() {
		cancel()
		cmd.Wait()
	}

	return addr, cleanup
}

// setupP9Test starts a test server and creates a P9Client for testing.
// This is the main entry point for P9 protocol tests.
func setupP9Test(t *testing.T, exposedPaths []string) (client *P9Client, cleanup func()) {
	t.Helper()

	addr, serverCleanup := startTestServer(t, exposedPaths)

	// Create P9 client
	client, err := NewP9Client(addr)
	if err != nil {
		serverCleanup()
		t.Fatalf("Failed to create P9 client: %v", err)
	}

	cleanup = func() {
		client.Close()
		serverCleanup()
	}

	return client, cleanup
}

// createTempFileTree creates a test directory with files and returns the root path.
// The files map uses relative paths as keys and file content as values.
// Directory paths should end with "/" to create empty directories.
func createTempFileTree(t *testing.T, files map[string]string) string {
	t.Helper()
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

// assertHostFileContent reads a file from the host filesystem and verifies its content.
func assertHostFileContent(t *testing.T, path string, expected []byte) {
	t.Helper()
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read file %s: %v", path, err)
	}
	if !bytes.Equal(actual, expected) {
		if len(expected) > 100 || len(actual) > 100 {
			t.Errorf("Content mismatch in %s: got %d bytes, want %d bytes", path, len(actual), len(expected))
		} else {
			t.Errorf("Content mismatch in %s: got %q, want %q", path, actual, expected)
		}
	}
}

// assertHostFileExists verifies a file exists on the host filesystem.
func assertHostFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("File %s does not exist: %v", path, err)
	}
}

// assertHostFileNotExists verifies a file does not exist on the host filesystem.
func assertHostFileNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("File %s should not exist but does (err=%v)", path, err)
	}
}

// assertHostFileMode verifies a file has the expected permission mode.
func assertHostFileMode(t *testing.T, path string, expectedMode os.FileMode) {
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

// assertHostFileSize verifies a file has the expected size.
func assertHostFileSize(t *testing.T, path string, expectedSize int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat file %s: %v", path, err)
	}
	if info.Size() != expectedSize {
		t.Errorf("Size mismatch for %s: got %d, want %d", path, info.Size(), expectedSize)
	}
}

// assertSymlinkTarget verifies a symlink points to the expected target.
func assertSymlinkTarget(t *testing.T, path string, expectedTarget string) {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("Failed to read symlink %s: %v", path, err)
	}
	if target != expectedTarget {
		t.Errorf("Symlink target mismatch for %s: got %q, want %q", path, target, expectedTarget)
	}
}

// assertIsDir verifies a path is a directory.
func assertIsDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat path %s: %v", path, err)
	}
	if !info.IsDir() {
		t.Errorf("Path %s is not a directory", path)
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

