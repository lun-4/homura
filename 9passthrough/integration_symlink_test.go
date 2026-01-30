package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
)

func TestIntegration_Symlink_Create(t *testing.T) {
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

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Create symlink
	_, err = dir.Symlink("file.txt", "link.txt", p9.NoUID, p9.NoGID)
	if err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	// Verify symlink exists on host
	linkPath := filepath.Join(tempDir, "link.txt")
	assertFileExists(t, linkPath)

	// Verify it's a symlink
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat failed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("Created file is not a symlink")
	}
}

func TestIntegration_Symlink_WithTarget(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"target.txt": "target content",
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

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Create symlink pointing to target.txt
	_, err = dir.Symlink("target.txt", "mylink", p9.NoUID, p9.NoGID)
	if err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	// Verify symlink target
	linkPath := filepath.Join(tempDir, "mylink")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink failed: %v", err)
	}
	if target != "target.txt" {
		t.Errorf("Symlink target is %q, expected %q", target, "target.txt")
	}
}

func TestIntegration_Symlink_InVirtualAncestor(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"deep/path/file.txt": "content",
	})

	registry := NewPathRegistry()
	deepPath := filepath.Join(tempDir, "deep", "path")
	if err := registry.AddPath(deepPath, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to virtual ancestor
	virtualPath := append(splitPath(tempDir), "deep")
	_, virt, err := root.Walk(virtualPath)
	if err != nil {
		t.Fatalf("Walk to virtual ancestor failed: %v", err)
	}
	defer virt.Close()

	// Try to create symlink in virtual ancestor - should fail
	_, err = virt.Symlink("target", "link", p9.NoUID, p9.NoGID)
	if err == nil {
		t.Fatal("Expected error when creating symlink in virtual ancestor")
	}
	assertSyscallError(t, err, syscall.EPERM)
}

func TestIntegration_Readlink_ValidSymlink(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"target.txt": "content",
	})

	// Create symlink on host filesystem
	linkPath := filepath.Join(tempDir, "mylink")
	if err := os.Symlink("target.txt", linkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to symlink
	linkComponents := append(splitPath(tempDir), "mylink")
	_, link, err := root.Walk(linkComponents)
	if err != nil {
		t.Fatalf("Walk to symlink failed: %v", err)
	}
	defer link.Close()

	// Read symlink target
	target, err := link.Readlink()
	if err != nil {
		t.Fatalf("Readlink failed: %v", err)
	}

	if target != "target.txt" {
		t.Errorf("Readlink returned %q, expected %q", target, "target.txt")
	}
}

func TestIntegration_Readlink_RegularFile(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"regular.txt": "not a link",
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

	// Walk to regular file
	fileComponents := append(splitPath(tempDir), "regular.txt")
	_, file, err := root.Walk(fileComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Try to readlink on regular file - should fail
	_, err = file.Readlink()
	if err == nil {
		t.Fatal("Expected error when reading link on regular file")
	}
	assertSyscallError(t, err, syscall.EINVAL)
}

func TestIntegration_Symlink_FollowDuringWalk(t *testing.T) {
	t.Skip("9P protocol does not automatically follow symlinks during Walk")
	tempDir := createTempFileTree(t, map[string]string{
		"subdir/target.txt": "content",
	})

	// Create symlink pointing to subdir
	linkPath := filepath.Join(tempDir, "dirlink")
	if err := os.Symlink("subdir", linkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk through symlink to target.txt
	linkComponents := append(splitPath(tempDir), "dirlink", "target.txt")
	_, file, err := root.Walk(linkComponents)
	if err != nil {
		t.Fatalf("Walk through symlink failed: %v", err)
	}
	defer file.Close()

	// Verify we can read the file
	_, _, err = file.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := file.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("ReadAt failed: %v", err)
	}

	if string(buf[:n]) != "content" {
		t.Errorf("Read %q, expected %q", buf[:n], "content")
	}

	// Verify on host that we can reach the file through symlink
	linkTargetPath := filepath.Join(tempDir, "dirlink", "target.txt")
	assertFileContent(t, linkTargetPath, []byte("content"))
}

func TestIntegration_Symlink_Dangling(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	// Create dangling symlink
	linkPath := filepath.Join(tempDir, "broken")
	if err := os.Symlink("nonexistent", linkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to broken symlink - the symlink itself should be accessible
	linkComponents := append(splitPath(tempDir), "broken")
	_, link, err := root.Walk(linkComponents)
	if err != nil {
		t.Fatalf("Walk to broken symlink failed: %v", err)
	}
	defer link.Close()

	// Readlink should work even if target doesn't exist
	target, err := link.Readlink()
	if err != nil {
		t.Fatalf("Readlink on broken symlink failed: %v", err)
	}

	if target != "nonexistent" {
		t.Errorf("Readlink returned %q, expected %q", target, "nonexistent")
	}
}

func TestIntegration_Symlink_Absolute(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"target.txt": "content",
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

	pathComponents := splitPath(tempDir)
	_, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	// Create symlink with absolute path
	absoluteTarget := filepath.Join(tempDir, "target.txt")
	_, err = dir.Symlink(absoluteTarget, "abslink", p9.NoUID, p9.NoGID)
	if err != nil {
		t.Fatalf("Symlink with absolute target failed: %v", err)
	}

	// Verify symlink target on host
	linkPath := filepath.Join(tempDir, "abslink")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink failed: %v", err)
	}
	if target != absoluteTarget {
		t.Errorf("Symlink target is %q, expected %q", target, absoluteTarget)
	}
}

// Hard link tests

func TestIntegration_Link_CreateHardLink(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"original.txt": "content",
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

	// Walk to directory
	dirComponents := splitPath(tempDir)
	_, dir, err := root.Walk(dirComponents)
	if err != nil {
		t.Fatalf("Walk to dir failed: %v", err)
	}
	defer dir.Close()

	// Walk to original file
	fileComponents := append(splitPath(tempDir), "original.txt")
	_, origFile, err := root.Walk(fileComponents)
	if err != nil {
		t.Fatalf("Walk to file failed: %v", err)
	}
	defer origFile.Close()

	// Create hard link - call Link on the directory, pass the file as target
	err = dir.Link(origFile, "hardlink.txt")
	if err != nil {
		t.Fatalf("Link failed: %v", err)
	}

	// Verify hard link exists on host
	linkPath := filepath.Join(tempDir, "hardlink.txt")
	assertFileExists(t, linkPath)

	// Verify content is the same
	assertFileContent(t, linkPath, []byte("content"))
}

func TestIntegration_Link_VerifyInode(t *testing.T) {
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

	dirComponents := splitPath(tempDir)
	_, dir, err := root.Walk(dirComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer dir.Close()

	fileComponents := append(splitPath(tempDir), "file.txt")
	_, origFile, err := root.Walk(fileComponents)
	if err != nil {
		t.Fatalf("Walk to file failed: %v", err)
	}
	defer origFile.Close()

	// Get original inode
	_, _, origAttr, err := origFile.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr on original failed: %v", err)
	}

	// Create hard link - call Link on the directory
	err = dir.Link(origFile, "link.txt")
	if err != nil {
		t.Fatalf("Link failed: %v", err)
	}

	// Walk to hard link
	linkComponents := append(splitPath(tempDir), "link.txt")
	_, linkFile, err := root.Walk(linkComponents)
	if err != nil {
		t.Fatalf("Walk to link failed: %v", err)
	}
	defer linkFile.Close()

	// Get link inode
	_, _, linkAttr, err := linkFile.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr on link failed: %v", err)
	}

	// Verify nlink count increased (hard links share the same inode)
	if linkAttr.NLink < 2 {
		t.Errorf("NLink is %d, expected at least 2", linkAttr.NLink)
	}

	// Verify same size (another indicator they're the same file)
	if origAttr.Size != linkAttr.Size {
		t.Errorf("Sizes differ: orig=%d, link=%d", origAttr.Size, linkAttr.Size)
	}
}

func TestIntegration_Link_CrossDirectory(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"dir1/file.txt": "content",
		"dir2/":         "",
	})

	// Expose both directories
	registry := NewPathRegistry()
	dir1Path := filepath.Join(tempDir, "dir1")
	dir2Path := filepath.Join(tempDir, "dir2")
	if err := registry.AddPath(dir1Path, false); err != nil {
		t.Fatalf("Failed to add dir1: %v", err)
	}
	if err := registry.AddPath(dir2Path, false); err != nil {
		t.Fatalf("Failed to add dir2: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to file in dir1
	fileComponents := append(splitPath(tempDir), "dir1", "file.txt")
	_, file, err := root.Walk(fileComponents)
	if err != nil {
		t.Fatalf("Walk to file failed: %v", err)
	}
	defer file.Close()

	// Walk to dir2
	dir2Components := append(splitPath(tempDir), "dir2")
	_, dir2, err := root.Walk(dir2Components)
	if err != nil {
		t.Fatalf("Walk to dir2 failed: %v", err)
	}
	defer dir2.Close()

	// Create hard link in dir2 - call Link on the directory
	err = dir2.Link(file, "linked.txt")
	if err != nil {
		t.Fatalf("Link across directories failed: %v", err)
	}

	// Verify link exists in dir2
	linkPath := filepath.Join(dir2Path, "linked.txt")
	assertFileContent(t, linkPath, []byte("content"))
}

func TestIntegration_Link_InVirtualAncestor(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"deep/path/file.txt": "content",
	})

	registry := NewPathRegistry()
	deepPath := filepath.Join(tempDir, "deep", "path")
	if err := registry.AddPath(deepPath, false); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to file
	fileComponents := append(splitPath(tempDir), "deep", "path", "file.txt")
	_, file, err := root.Walk(fileComponents)
	if err != nil {
		t.Fatalf("Walk to file failed: %v", err)
	}
	defer file.Close()

	// Walk to virtual ancestor
	virtComponents := append(splitPath(tempDir), "deep")
	_, virt, err := root.Walk(virtComponents)
	if err != nil {
		t.Fatalf("Walk to virtual ancestor failed: %v", err)
	}
	defer virt.Close()

	// Try to create hard link in virtual ancestor - should fail
	// Call Link on the virtual directory
	err = virt.Link(file, "link.txt")
	if err == nil {
		t.Fatal("Expected error when creating link in virtual ancestor")
	}
	assertSyscallError(t, err, syscall.EPERM)
}
