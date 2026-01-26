package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
	"golang.org/x/sys/unix"
)

// Extended attribute tests

func TestIntegration_GetXattr_UserAttr(t *testing.T) {
	t.Skip("p9 library v0.3.0 client_file.go has xattr methods stubbed to return ENOSYS - not implemented in protocol")
	skipIfNoXattrSupport(t)

	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	// Set an xattr on the host file
	filePath := filepath.Join(tempDir, "file.txt")
	xattrName := "user.test"
	xattrValue := []byte("test value")
	if err := unix.Setxattr(filePath, xattrName, xattrValue, 0); err != nil {
		t.Fatalf("Failed to set xattr: %v", err)
	}

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to file
	pathComponents := append(splitPath(tempDir), "file.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Get xattr
	buf, err := file.GetXattr(xattrName)
	if err != nil {
		t.Fatalf("GetXattr failed: %v", err)
	}

	if !bytes.Equal(buf, xattrValue) {
		t.Errorf("GetXattr returned %q, expected %q", buf, xattrValue)
	}
}

func TestIntegration_SetXattr_Create(t *testing.T) {
	t.Skip("p9 library v0.3.0 client_file.go has xattr methods stubbed to return ENOSYS - not implemented in protocol")
	skipIfNoXattrSupport(t)

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

	pathComponents := append(splitPath(tempDir), "file.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Set xattr
	xattrName := "user.newattr"
	xattrValue := []byte("new value")
	err = file.SetXattr(xattrName, xattrValue, 0)
	if err != nil {
		t.Fatalf("SetXattr failed: %v", err)
	}

	// Verify on host filesystem
	filePath := filepath.Join(tempDir, "file.txt")
	buf := make([]byte, 1024)
	n, err := unix.Getxattr(filePath, xattrName, buf)
	if err != nil {
		t.Fatalf("Getxattr on host failed: %v", err)
	}

	if !bytes.Equal(buf[:n], xattrValue) {
		t.Errorf("Xattr value is %q, expected %q", buf[:n], xattrValue)
	}
}

func TestIntegration_SetXattr_Replace(t *testing.T) {
	t.Skip("p9 library v0.3.0 client_file.go has xattr methods stubbed to return ENOSYS - not implemented in protocol")
	skipIfNoXattrSupport(t)

	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	filePath := filepath.Join(tempDir, "file.txt")
	xattrName := "user.replace"
	oldValue := []byte("old value")
	if err := unix.Setxattr(filePath, xattrName, oldValue, 0); err != nil {
		t.Fatalf("Failed to set initial xattr: %v", err)
	}

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := append(splitPath(tempDir), "file.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Replace xattr
	newValue := []byte("new value")
	err = file.SetXattr(xattrName, newValue, 0)
	if err != nil {
		t.Fatalf("SetXattr failed: %v", err)
	}

	// Verify on host filesystem
	buf := make([]byte, 1024)
	n, err := unix.Getxattr(filePath, xattrName, buf)
	if err != nil {
		t.Fatalf("Getxattr on host failed: %v", err)
	}

	if !bytes.Equal(buf[:n], newValue) {
		t.Errorf("Xattr value is %q, expected %q", buf[:n], newValue)
	}
}

func TestIntegration_ListXattrs_Multiple(t *testing.T) {
	t.Skip("p9 library v0.3.0 client_file.go has xattr methods stubbed to return ENOSYS - not implemented in protocol")
	skipIfNoXattrSupport(t)

	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	// Set multiple xattrs on host
	filePath := filepath.Join(tempDir, "file.txt")
	xattrs := map[string][]byte{
		"user.attr1": []byte("value1"),
		"user.attr2": []byte("value2"),
		"user.attr3": []byte("value3"),
	}
	for name, value := range xattrs {
		if err := unix.Setxattr(filePath, name, value, 0); err != nil {
			t.Fatalf("Failed to set xattr %s: %v", name, err)
		}
	}

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := append(splitPath(tempDir), "file.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// List xattrs
	attrList, err := file.ListXattrs()
	if err != nil {
		t.Fatalf("ListXattrs failed: %v", err)
	}

	// Check that all expected xattrs are in the list
	foundCount := 0
	for expectedName := range xattrs {
		for _, name := range attrList {
			if name == expectedName {
				foundCount++
				break
			}
		}
	}

	if foundCount != len(xattrs) {
		t.Errorf("Found %d xattrs in list, expected %d (list: %v)", foundCount, len(xattrs), attrList)
	}
}

func TestIntegration_RemoveXattr_Existing(t *testing.T) {
	t.Skip("p9 library v0.3.0 client_file.go has xattr methods stubbed to return ENOSYS - not implemented in protocol")
	skipIfNoXattrSupport(t)

	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "content",
	})

	filePath := filepath.Join(tempDir, "file.txt")
	xattrName := "user.remove"
	if err := unix.Setxattr(filePath, xattrName, []byte("value"), 0); err != nil {
		t.Fatalf("Failed to set xattr: %v", err)
	}

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := append(splitPath(tempDir), "file.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Remove xattr
	err = file.RemoveXattr(xattrName)
	if err != nil {
		t.Fatalf("RemoveXattr failed: %v", err)
	}

	// Verify removed on host
	_, err = unix.Getxattr(filePath, xattrName, nil)
	if err == nil {
		t.Fatal("Expected xattr to be removed, but it still exists")
	}
	// On Linux, ENODATA is returned when an xattr doesn't exist
	if !errors.Is(err, syscall.ENODATA) {
		t.Logf("Got error %v (expected ENODATA, but may vary by platform)", err)
	}
}

func TestIntegration_Xattr_VirtualAncestor(t *testing.T) {
	t.Skip("p9 library v0.3.0 client_file.go has xattr methods stubbed to return ENOSYS - not implemented in protocol")
	skipIfNoXattrSupport(t)

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

	// Walk to virtual ancestor
	virtualPath := append(splitPath(tempDir), "deep")
	_, virt, err := root.Walk(virtualPath)
	if err != nil {
		t.Fatalf("Walk to virtual ancestor failed: %v", err)
	}
	defer virt.Close()

	// Try to set xattr on virtual ancestor - should fail
	err = virt.SetXattr("user.test", []byte("value"), 0)
	if err == nil {
		t.Fatal("Expected error when setting xattr on virtual ancestor")
	}
	assertSyscallError(t, err, syscall.EPERM)
}

// Protocol edge cases and large operations

func TestIntegration_LargeFile_ReadWrite(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"large.dat": "",
	})

	// Create large file (100MB)
	largePath := filepath.Join(tempDir, "large.dat")
	createLargeFile(t, largePath, 100*1024*1024)

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	pathComponents := append(splitPath(tempDir), "large.dat")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Read 1MB from different offsets
	buf := make([]byte, 1024*1024)
	offsets := []int64{0, 50 * 1024 * 1024, 99 * 1024 * 1024}

	for _, offset := range offsets {
		n, err := file.ReadAt(buf, offset)
		if err != nil && n == 0 {
			t.Fatalf("ReadAt at offset %d failed: %v", offset, err)
		}
		if n != len(buf) {
			t.Errorf("Read %d bytes at offset %d, expected %d", n, offset, len(buf))
		}
	}
}

func TestIntegration_Readdir_Pagination(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file01.txt": "1",
		"file02.txt": "2",
		"file03.txt": "3",
		"file04.txt": "4",
		"file05.txt": "5",
		"file06.txt": "6",
		"file07.txt": "7",
		"file08.txt": "8",
		"file09.txt": "9",
		"file10.txt": "10",
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

	_, _, err = dir.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Read directory in chunks
	// Note: count is in bytes, not number of entries
	// Use a small byte count to test pagination (e.g., 200 bytes per chunk)
	allDirents := []p9.Dirent{}
	offset := uint64(0)
	for {
		dirents, err := dir.Readdir(offset, 200)
		if err != nil {
			t.Fatalf("Readdir failed: %v", err)
		}
		if len(dirents) == 0 {
			break
		}
		allDirents = append(allDirents, dirents...)
		// Update offset to last entry's offset (not +1)
		// The Offset field contains the offset to use for continuing after this entry
		if len(dirents) > 0 {
			offset = dirents[len(dirents)-1].Offset
		}
	}

	// Should have read all 10 files
	if len(allDirents) != 10 {
		t.Errorf("Read %d entries total, expected 10", len(allDirents))
	}
}

func TestIntegration_Readdir_LargeDirectory(t *testing.T) {
	tempDir := t.TempDir()

	// Create 100 files (limited by 8KB message size in p9.Client)
	numFiles := 100
	for i := 0; i < numFiles; i++ {
		filename := filepath.Join(tempDir, fmt.Sprintf("file%04d.txt", i))
		if err := os.WriteFile(filename, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
	}

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

	_, _, err = dir.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Read entries (9P message size limits may prevent reading all at once)
	dirents, err := dir.Readdir(0, 1000)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Verify we got a reasonable number of entries
	// The 8KB message size limit means we can't get all 100 in one call
	if len(dirents) < 20 {
		t.Errorf("Read only %d entries, expected at least 20 from directory with %d files", len(dirents), numFiles)
	}
	t.Logf("Read %d entries in single Readdir call (out of %d files, limited by 8KB message size)", len(dirents), numFiles)
}

func TestIntegration_QID_Consistency(t *testing.T) {
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

	pathComponents := append(splitPath(tempDir), "file.txt")

	// Walk to file multiple times and verify QID is consistent
	qids := []p9.QID{}
	for i := 0; i < 3; i++ {
		walkQids, file, err := root.Walk(pathComponents)
		if err != nil {
			t.Fatalf("Walk %d failed: %v", i, err)
		}
		if len(walkQids) > 0 {
			qids = append(qids, walkQids[len(walkQids)-1])
		}
		file.Close()
	}

	// All QIDs should be identical
	for i := 1; i < len(qids); i++ {
		if qids[i] != qids[0] {
			t.Errorf("QID %d differs: %+v vs %+v", i, qids[i], qids[0])
		}
	}
}

func TestIntegration_WalkGetAttr_Optimization(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"dir/file.txt": "content",
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

	// WalkGetAttr should walk and return attributes in one call
	pathComponents := append(splitPath(tempDir), "dir", "file.txt")
	qids, file, valid, attr, err := root.WalkGetAttr(pathComponents)
	if err != nil {
		t.Fatalf("WalkGetAttr failed: %v", err)
	}
	defer file.Close()

	if len(qids) != len(pathComponents) {
		t.Errorf("Got %d QIDs, expected %d", len(qids), len(pathComponents))
	}

	// Should have valid attributes
	if !valid.Mode {
		t.Error("Mode attribute not valid")
	}
	if !valid.Size {
		t.Error("Size attribute not valid")
	}

	// Verify attributes match file
	if attr.Size != 7 { // "content" is 7 bytes
		t.Errorf("Size is %d, expected 7", attr.Size)
	}
}

func TestIntegration_StatFS_ExposedPath(t *testing.T) {
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

	// StatFS should return filesystem stats
	statfs, err := dir.StatFS()
	if err != nil {
		t.Fatalf("StatFS failed: %v", err)
	}

	// Basic sanity checks
	if statfs.BlockSize == 0 {
		t.Error("BlockSize is 0")
	}
	if statfs.Blocks == 0 {
		t.Error("Blocks is 0")
	}
}

func TestIntegration_StatFS_VirtualAncestor(t *testing.T) {
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

	// Walk to virtual ancestor
	virtualPath := append(splitPath(tempDir), "deep")
	_, virt, err := root.Walk(virtualPath)
	if err != nil {
		t.Fatalf("Walk to virtual ancestor failed: %v", err)
	}
	defer virt.Close()

	// StatFS on virtual directory - should return synthetic stats or error
	_, err = virt.StatFS()
	if err != nil {
		// It's acceptable for virtual directories to return an error for StatFS
		t.Logf("StatFS on virtual ancestor returned error (acceptable): %v", err)
	}
}

func TestIntegration_MultipleClients(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "shared content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	// Connect multiple clients
	_, root1, cleanup1 := connectTestClient(t, listener.Addr().String())
	defer cleanup1()
	_, root2, cleanup2 := connectTestClient(t, listener.Addr().String())
	defer cleanup2()

	pathComponents := append(splitPath(tempDir), "file.txt")

	// Both clients should be able to access the same file
	_, file1, err := root1.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Client 1 walk failed: %v", err)
	}
	defer file1.Close()

	_, file2, err := root2.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Client 2 walk failed: %v", err)
	}
	defer file2.Close()

	// Both should be able to read
	_, _, err = file1.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Client 1 open failed: %v", err)
	}

	_, _, err = file2.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Client 2 open failed: %v", err)
	}

	buf1 := make([]byte, 1024)
	n1, err := file1.ReadAt(buf1, 0)
	if err != nil && n1 == 0 {
		t.Fatalf("Client 1 read failed: %v", err)
	}

	buf2 := make([]byte, 1024)
	n2, err := file2.ReadAt(buf2, 0)
	if err != nil && n2 == 0 {
		t.Fatalf("Client 2 read failed: %v", err)
	}

	// Both should read same content
	if !bytes.Equal(buf1[:n1], buf2[:n2]) {
		t.Error("Clients read different content")
	}
}

func TestIntegration_ConcurrentReads(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"file.txt": "concurrent read content",
	})

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	// Create multiple clients
	numClients := runtime.NumCPU()
	if numClients < 2 {
		numClients = 2
	}

	done := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			_, root, cleanup := connectTestClient(t, listener.Addr().String())
			defer cleanup()

			pathComponents := append(splitPath(tempDir), "file.txt")
			_, file, err := root.Walk(pathComponents)
			if err != nil {
				done <- err
				return
			}
			defer file.Close()

			_, _, err = file.Open(p9.ReadOnly)
			if err != nil {
				done <- err
				return
			}

			// Read multiple times
			for j := 0; j < 10; j++ {
				buf := make([]byte, 1024)
				n, err := file.ReadAt(buf, 0)
				if err != nil && n == 0 {
					done <- err
					return
				}
				if string(buf[:n]) != "concurrent read content" {
					done <- err
					return
				}
			}

			done <- nil
		}(i)
	}

	// Wait for all clients
	for i := 0; i < numClients; i++ {
		err := <-done
		if err != nil {
			t.Errorf("Client %d failed: %v", i, err)
		}
	}
}

func TestIntegration_ConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()

	// Create separate files for each writer
	numWriters := runtime.NumCPU()
	if numWriters < 2 {
		numWriters = 2
	}

	for i := 0; i < numWriters; i++ {
		filename := filepath.Join(tempDir, "file"+string(rune('0'+i))+".txt")
		if err := os.WriteFile(filename, []byte("initial"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
	}

	registry := NewPathRegistry()
	if err := registry.AddPath(tempDir); err != nil {
		t.Fatalf("Failed to add path: %v", err)
	}

	listener, _, cleanup := startTestServer(t, registry)
	defer cleanup()
	time.Sleep(100 * time.Millisecond)

	done := make(chan error, numWriters)

	for i := 0; i < numWriters; i++ {
		go func(writerID int) {
			_, root, cleanup := connectTestClient(t, listener.Addr().String())
			defer cleanup()

			filename := "file" + string(rune('0'+writerID)) + ".txt"
			pathComponents := append(splitPath(tempDir), filename)
			_, file, err := root.Walk(pathComponents)
			if err != nil {
				done <- err
				return
			}
			defer file.Close()

			_, _, err = file.Open(p9.ReadWrite)
			if err != nil {
				done <- err
				return
			}

			// Write multiple times
			for j := 0; j < 10; j++ {
				data := []byte("writer " + string(rune('0'+writerID)) + " write " + string(rune('0'+j)))
				_, err := file.WriteAt(data, 0)
				if err != nil {
					done <- err
					return
				}
			}

			done <- nil
		}(i)
	}

	// Wait for all writers
	for i := 0; i < numWriters; i++ {
		err := <-done
		if err != nil {
			t.Errorf("Writer %d failed: %v", i, err)
		}
	}
}
