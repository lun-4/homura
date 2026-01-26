package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
)

func TestIntegration_Mkdir_InExposedDir(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to exposed directory
	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Create a new subdirectory
	_, err = dir.Mkdir("newdir", 0755, p9.NoUID, p9.NoGID)
	if err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

	// Verify directory exists on host
	hostPath := filepath.Join(tempDir, "newdir")
	assertFileExists(t, hostPath)

	// Verify it's a directory
	info, err := os.Stat(hostPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if !info.IsDir() {
		t.Error("Created path is not a directory")
	}
}

func TestIntegration_Mkdir_WithMode(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Create directory with specific mode
	_, err = dir.Mkdir("testdir", 0700, p9.NoUID, p9.NoGID)
	if err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

	// Verify mode on host
	hostPath := filepath.Join(tempDir, "testdir")
	assertFileMode(t, hostPath, 0700)
}

func TestIntegration_Mkdir_InVirtualAncestor(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"deep/path/file.txt": "content",
	})

	registry := NewPathRegistry()
	deepPath := filepath.Join(tempDir, "deep", "path")
	if err := registry.AddPath(deepPath); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to virtual ancestor "deep"
	virtualPath := append(splitPath(tempDir), "deep")
	_, virt, err := root.Walk(virtualPath)
	if err != nil {
		t.Fatalf("Walk to virtual ancestor failed: %v", err)
	}
	defer virt.Close()

	// Try to create directory in virtual ancestor - should fail
	_, err = virt.Mkdir("newdir", 0755, p9.NoUID, p9.NoGID)
	if err == nil {
		t.Fatal("Expected error when creating directory in virtual ancestor")
	}
	assertSyscallError(t, err, syscall.EPERM)
}

func TestIntegration_Mkdir_AlreadyExists(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"existing/": "",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Try to create directory that already exists
	_, err = dir.Mkdir("existing", 0755, p9.NoUID, p9.NoGID)
	if err == nil {
		t.Fatal("Expected error when creating existing directory")
	}
	assertSyscallError(t, err, syscall.EEXIST)
}

func TestIntegration_Mkdir_ThenWalk(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Create new directory
	_, err = dir.Mkdir("newdir", 0755, p9.NoUID, p9.NoGID)
	if err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

	// Now walk into it
	_, newdir, err := dir.Walk([]string{"newdir"})
	if err != nil {
		t.Fatalf("Walk to newly created directory failed: %v", err)
	}
	defer newdir.Close()

	// Verify it's a directory
	_, _, attr, err := newdir.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}
	if !attr.Mode.IsDir() {
		t.Error("Expected directory mode")
	}
}

func TestIntegration_UnlinkAt_File(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file1.txt": "content1",
		"file2.txt": "content2",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Unlink file1.txt
	err = dir.UnlinkAt("file1.txt", 0)
	if err != nil {
		t.Fatalf("UnlinkAt failed: %v", err)
	}

	// Verify file no longer exists on host
	hostPath := filepath.Join(tempDir, "file1.txt")
	assertFileNotExists(t, hostPath)

	// Verify file2.txt still exists
	hostPath2 := filepath.Join(tempDir, "file2.txt")
	assertFileExists(t, hostPath2)
}

func TestIntegration_UnlinkAt_EmptyDirectory(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"emptydir/": "",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Unlink empty directory (with AT_REMOVEDIR flag if needed)
	// Note: The implementation uses os.Remove which handles both files and dirs
	err = dir.UnlinkAt("emptydir", 0)
	if err != nil {
		t.Fatalf("UnlinkAt failed: %v", err)
	}

	// Verify directory no longer exists on host
	hostPath := filepath.Join(tempDir, "emptydir")
	assertFileNotExists(t, hostPath)
}

func TestIntegration_UnlinkAt_NonEmptyDir(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"nonempty/file.txt": "content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Try to unlink non-empty directory - should fail
	err = dir.UnlinkAt("nonempty", 0)
	if err == nil {
		t.Fatal("Expected error when unlinking non-empty directory")
	}
	// os.Remove can return different errors for non-empty directories
	// Just verify we got an error and the directory still exists

	// Verify directory still exists
	hostPath := filepath.Join(tempDir, "nonempty")
	assertFileExists(t, hostPath)
}

func TestIntegration_UnlinkAt_VirtualAncestor(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"deep/path/file.txt": "content",
	})

	registry := NewPathRegistry()
	deepPath := filepath.Join(tempDir, "deep", "path")
	if err := registry.AddPath(deepPath); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to tempDir (virtual ancestor)
	virtualPath := splitPath(tempDir)
	_, virt, err := root.Walk(virtualPath)
	if err != nil {
		t.Fatalf("Walk to virtual ancestor failed: %v", err)
	}
	defer virt.Close()

	// Try to unlink "deep" directory (which has exposed descendants)
	err = virt.UnlinkAt("deep", 0)
	if err == nil {
		t.Fatal("Expected error when unlinking in virtual ancestor")
	}
	assertSyscallError(t, err, syscall.EPERM)
}

func TestIntegration_RenameAt_SameDir(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"oldname.txt": "content to rename",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Rename within same directory
	err = dir.RenameAt("oldname.txt", dir, "newname.txt")
	if err != nil {
		t.Fatalf("RenameAt failed: %v", err)
	}

	// Verify old name doesn't exist
	oldPath := filepath.Join(tempDir, "oldname.txt")
	assertFileNotExists(t, oldPath)

	// Verify new name exists with same content
	newPath := filepath.Join(tempDir, "newname.txt")
	assertFileContent(t, newPath, []byte("content to rename"))
}

func TestIntegration_RenameAt_AcrossDirs(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"dir1/file.txt": "content",
		"dir2/":         "",
	})

	// Expose both directories
	registry := NewPathRegistry()
	dir1Path := filepath.Join(tempDir, "dir1")
	dir2Path := filepath.Join(tempDir, "dir2")
	if err := registry.AddPath(dir1Path); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}
	if err := registry.AddPath(dir2Path); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to dir1
	dir1Components := append(splitPath(tempDir), "dir1")
	_, dir1, err := root.Walk(dir1Components)
	if err != nil {
		t.Fatalf("Walk to dir1 failed: %v", err)
	}
	defer dir1.Close()

	// Walk to dir2
	dir2Components := append(splitPath(tempDir), "dir2")
	_, dir2, err := root.Walk(dir2Components)
	if err != nil {
		t.Fatalf("Walk to dir2 failed: %v", err)
	}
	defer dir2.Close()

	// Rename from dir1 to dir2
	err = dir1.RenameAt("file.txt", dir2, "moved.txt")
	if err != nil {
		t.Fatalf("RenameAt across directories failed: %v", err)
	}

	// Verify old location doesn't exist
	oldPath := filepath.Join(dir1Path, "file.txt")
	assertFileNotExists(t, oldPath)

	// Verify new location exists
	newPath := filepath.Join(dir2Path, "moved.txt")
	assertFileContent(t, newPath, []byte("content"))
}

func TestIntegration_RenameAt_OverwriteExisting(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"source.txt": "new content",
		"dest.txt":   "old content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Rename source to dest (overwriting dest)
	err = dir.RenameAt("source.txt", dir, "dest.txt")
	if err != nil {
		t.Fatalf("RenameAt failed: %v", err)
	}

	// Verify source doesn't exist
	sourcePath := filepath.Join(tempDir, "source.txt")
	assertFileNotExists(t, sourcePath)

	// Verify dest has new content
	destPath := filepath.Join(tempDir, "dest.txt")
	assertFileContent(t, destPath, []byte("new content"))
}
