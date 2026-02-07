package main

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
)

func TestIntegration_OpenFileUnexpose(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content that was accessible",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to and open the file
	pathComponents := append(splitPath(tempDir), "file.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Verify file is readable before unexposing
	buf := make([]byte, 1024)
	n, err := file.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("ReadAt before unexpose failed: %v", err)
	}

	// Now unexpose the path
	registry.RemovePath(tempDir)

	// Path-based operations should fail after unexpose
	_, _, _, err = file.GetAttr(p9.AttrMaskAll)
	if err == nil {
		t.Fatal("Expected GetAttr to fail after unexpose, but it succeeded")
	}
	assertSyscallError(t, err, syscall.ENOENT)

	// However, ReadAt continues to work on the open file handle (standard Unix behavior)
	// The file descriptor remains valid even if the path is unexposed
	n, err = file.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("ReadAt on open file handle failed: %v", err)
	}

	if string(buf[:n]) != "content that was accessible" {
		t.Errorf("Read wrong content: %q", buf[:n])
	}
}

func TestIntegration_OpenDirUnexpose(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file1.txt": "content1",
		"file2.txt": "content2",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to and open the directory
	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	_, _, err = dir.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Read directory contents before unexposing
	dirents1, err := dir.Readdir(0, 100)
	if err != nil {
		t.Fatalf("Readdir before unexpose failed: %v", err)
	}
	if len(dirents1) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(dirents1))
	}

	// Now unexpose the path
	registry.RemovePath(tempDir)

	// Directory operations should fail after unexpose
	_, err = dir.Readdir(0, 100)
	if err == nil {
		t.Fatal("Expected Readdir to fail after unexpose, but it succeeded")
	}
}

func TestIntegration_WalkAfterUnexpose(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Verify we can walk to the file initially
	pathComponents := append(splitPath(tempDir), "file.txt")
	_, file1, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Initial walk failed: %v", err)
	}
	file1.Close()

	// Unexpose the path
	registry.RemovePath(tempDir)

	// Now walking to the file should fail
	_, _, err = root.Walk(pathComponents)
	if err == nil {
		t.Fatal("Expected walk to fail after unexposing, but it succeeded")
	}
	assertSyscallError(t, err, syscall.ENOENT)
}

func TestIntegration_ReExposeAfterUnexpose(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := append(splitPath(tempDir), "file.txt")

	// Walk to file - should succeed
	_, file1, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Initial walk failed: %v", err)
	}
	file1.Close()

	// Unexpose
	registry.RemovePath(tempDir)

	// Walk should fail
	_, _, err = root.Walk(pathComponents)
	if err == nil {
		t.Fatal("Expected walk to fail after unexpose")
	}

	// Re-expose
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to re-expose path: %v", err)
	}

	// Walk should succeed again
	_, file2, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk after re-expose failed: %v", err)
	}
	defer file2.Close()

	// Verify we can still read the file
	_, _, err = file2.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open after re-expose failed: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := file2.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("ReadAt failed: %v", err)
	}

	if string(buf[:n]) != "content" {
		t.Errorf("Read wrong content: %q", buf[:n])
	}
}

func TestIntegration_ExposeParentOfChild(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"parent/child/file.txt": "content",
	})

	registry := NewPathRegistry()

	// First expose child
	childPath := filepath.Join(tempDir, "parent", "child")
	if err := registry.AddPath(childPath, false); err != nil {
		t.Fatalf("Failed to expose child: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Verify we can walk to child
	childComponents := append(splitPath(tempDir), "parent", "child")
	_, child1, err := root.Walk(childComponents)
	if err != nil {
		t.Fatalf("Walk to child failed: %v", err)
	}
	child1.Close()

	// Now expose parent
	parentPath := filepath.Join(tempDir, "parent")
	if err := registry.AddPath(parentPath, false); err != nil {
		t.Fatalf("Failed to expose parent: %v", err)
	}

	// Child should still be accessible
	_, child2, err := root.Walk(childComponents)
	if err != nil {
		t.Fatalf("Walk to child after parent expose failed: %v", err)
	}
	child2.Close()

	// Parent should now be fully visible (not just virtual)
	parentComponents := append(splitPath(tempDir), "parent")
	_, parent, err := root.Walk(parentComponents)
	if err != nil {
		t.Fatalf("Walk to parent failed: %v", err)
	}
	defer parent.Close()

	// Open and read parent directory
	_, _, err = parent.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open parent failed: %v", err)
	}

	dirents, err := parent.Readdir(0, 100)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Should see "child" directory
	foundChild := false
	for _, dirent := range dirents {
		if dirent.Name == "child" {
			foundChild = true
		}
	}
	if !foundChild {
		t.Error("Child directory not found in parent listing")
	}
}

func TestIntegration_ExposeChildOfParent(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"parent/child/file.txt": "content",
		"parent/other.txt":      "other",
	})

	registry := NewPathRegistry()

	// First expose parent
	parentPath := filepath.Join(tempDir, "parent")
	if err := registry.AddPath(parentPath, false); err != nil {
		t.Fatalf("Failed to expose parent: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Verify we can see both child and other.txt
	parentComponents := append(splitPath(tempDir), "parent")
	_, parent, err := root.Walk(parentComponents)
	if err != nil {
		t.Fatalf("Walk to parent failed: %v", err)
	}
	defer parent.Close()

	_, _, err = parent.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	dirents1, err := parent.Readdir(0, 100)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Should see both "child" and "other.txt"
	if len(dirents1) != 2 {
		t.Errorf("Expected 2 entries in parent, got %d", len(dirents1))
	}

	// Now also expose child explicitly (redundant, but should work)
	childPath := filepath.Join(tempDir, "parent", "child")
	if err := registry.AddPath(childPath, false); err != nil {
		t.Fatalf("Failed to expose child: %v", err)
	}

	// Child should still be accessible
	childComponents := append(splitPath(tempDir), "parent", "child")
	_, child, err := root.Walk(childComponents)
	if err != nil {
		t.Fatalf("Walk to child failed: %v", err)
	}
	child.Close()
}

func TestIntegration_WriteAfterUnexpose(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "original",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to and open file for writing
	pathComponents := append(splitPath(tempDir), "file.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write before unexposing to verify it works
	data := []byte("written before unexpose!")
	n, err := file.WriteAt(data, 0)
	if err != nil {
		t.Fatalf("WriteAt before unexpose failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("Wrote %d bytes, expected %d", n, len(data))
	}

	// Unexpose the path
	registry.RemovePath(tempDir)

	// Write operations continue to work on open file handle (standard Unix behavior)
	// The file descriptor remains valid even if the path is unexposed
	data2 := []byte("written after unexpose!!")
	n, err = file.WriteAt(data2, 0)
	if err != nil {
		t.Fatalf("WriteAt on open file handle failed: %v", err)
	}
	if n != len(data2) {
		t.Errorf("Wrote %d bytes, expected %d", n, len(data2))
	}

	// Verify on host filesystem
	hostPath := filepath.Join(tempDir, "file.txt")
	assertFileContent(t, hostPath, data2)
}

func TestIntegration_NewOpenAfterUnexpose(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Unexpose immediately
	registry.RemovePath(tempDir)

	// Try to walk to file - should fail
	pathComponents := append(splitPath(tempDir), "file.txt")
	_, _, err := root.Walk(pathComponents)
	if err == nil {
		t.Fatal("Expected walk to fail after unexpose")
	}
	assertSyscallError(t, err, syscall.ENOENT)
}
