package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/hugelgupf/p9/p9"
)

// These tests validate the P9Client operations end-to-end:
// P9Client → 9passthrough server → host filesystem
//
// This approach tests the same integration without requiring FUSE kernel support.

// ============================================================================
// GetAttr Tests
// ============================================================================

func TestP9_GetAttr_File(t *testing.T) {
	content := "test content here"
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": content,
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Get file attributes
	filePath := filepath.Join(dir, "testfile.txt")
	_, attr, err := client.GetAttr(filePath)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	// Verify size
	if attr.Size != uint64(len(content)) {
		t.Errorf("Size mismatch: got %d, want %d", attr.Size, len(content))
	}

	// Verify it's a regular file
	if attr.Mode.IsDir() {
		t.Error("Expected regular file, got directory")
	}
}

func TestP9_GetAttr_Directory(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"subdir/": "",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Get directory attributes
	subdirPath := filepath.Join(dir, "subdir")
	_, attr, err := client.GetAttr(subdirPath)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	// Verify it's a directory
	if !attr.Mode.IsDir() {
		t.Error("Expected directory mode")
	}
}

func TestP9_GetAttr_NotFound(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"existing.txt": "content",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Try to get attributes for non-existent file
	nonexistentPath := filepath.Join(dir, "nonexistent.txt")
	_, _, err := client.GetAttr(nonexistentPath)
	if err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}
}

// ============================================================================
// Read/Write Tests
// ============================================================================

func TestP9_ReadWrite_Basic(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "testfile.txt")

	// Open for writing
	fid, _, err := client.Open(filePath, p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write data
	testData := []byte("Hello, P9 World!")
	n, err := client.WriteAt(fid, testData, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}
	if n != len(testData) {
		t.Errorf("WriteAt returned wrong count: got %d, want %d", n, len(testData))
	}

	// Close and reopen for reading
	client.CloseFID(fid)

	fid, _, err = client.Open(filePath, p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open for read failed: %v", err)
	}
	defer client.CloseFID(fid)

	// Read data back
	readData, err := client.ReadAt(fid, 0, uint32(len(testData)+10))
	if err != nil {
		t.Fatalf("ReadAt failed: %v", err)
	}

	if !bytes.Equal(readData, testData) {
		t.Errorf("Read data mismatch: got %q, want %q", readData, testData)
	}

	// Verify on host filesystem
	assertHostFileContent(t, filePath, testData)
}

func TestP9_ReadWrite_LargeFile(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"largefile.bin": "",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "largefile.bin")

	// Open for writing
	fid, _, err := client.Open(filePath, p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write 128KB of data (tests chunking)
	testData := bytes.Repeat([]byte("ABCDEFGH"), 16*1024) // 128KB
	offset := int64(0)
	remaining := testData

	for len(remaining) > 0 {
		chunk := remaining
		if len(chunk) > 32*1024 {
			chunk = remaining[:32*1024]
		}

		n, err := client.WriteAt(fid, chunk, uint64(offset))
		if err != nil {
			t.Fatalf("WriteAt failed at offset %d: %v", offset, err)
		}
		offset += int64(n)
		remaining = remaining[n:]
	}

	client.CloseFID(fid)

	// Verify on host filesystem
	assertHostFileContent(t, filePath, testData)
}

func TestP9_ReadWrite_Offset(t *testing.T) {
	initialContent := "0123456789"
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": initialContent,
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "testfile.txt")

	// Open for reading
	fid, _, err := client.Open(filePath, p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer client.CloseFID(fid)

	// Read at offset 5
	readData, err := client.ReadAt(fid, 5, 5)
	if err != nil {
		t.Fatalf("ReadAt failed: %v", err)
	}

	expected := []byte("56789")
	if !bytes.Equal(readData, expected) {
		t.Errorf("Read at offset mismatch: got %q, want %q", readData, expected)
	}
}

// ============================================================================
// Readdir Tests
// ============================================================================

func TestP9_Readdir_Basic(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"file1.txt": "content1",
		"file2.txt": "content2",
		"subdir/":   "",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Read directory
	entries, err := client.Readdir(dir)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Collect entries
	entryMap := make(map[string]bool)
	for _, e := range entries {
		entryMap[e.Name] = true
	}

	// Verify expected entries
	expectedEntries := []string{"file1.txt", "file2.txt", "subdir"}
	for _, name := range expectedEntries {
		if !entryMap[name] {
			t.Errorf("Missing expected entry: %s", name)
		}
	}
}

func TestP9_Readdir_SkipsDots(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "content",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Read directory
	entries, err := client.Readdir(dir)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Verify . and .. are not in the list
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			t.Errorf("Readdir should not return %q", e.Name)
		}
	}
}

// ============================================================================
// Create Tests
// ============================================================================

func TestP9_Create_NewFile(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "newfile.txt")

	// Create a new file
	fid, _, err := client.Create(filePath, 0644, uint32(syscall.O_RDWR))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Write to the file
	testData := []byte("new file content")
	_, err = client.WriteAt(fid, testData, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}
	client.CloseFID(fid)

	// Verify file exists on host
	assertHostFileExists(t, filePath)
	assertHostFileContent(t, filePath, testData)
}

// ============================================================================
// Mkdir Tests
// ============================================================================

func TestP9_Mkdir_NewDir(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	subdirPath := filepath.Join(dir, "newsubdir")

	// Create a new directory
	_, err := client.Mkdir(subdirPath, 0755|p9.ModeDirectory)
	if err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

	// Verify directory exists on host
	assertHostFileExists(t, subdirPath)
	assertIsDir(t, subdirPath)
}

// ============================================================================
// Unlink Tests
// ============================================================================

func TestP9_Unlink_File(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"todelete.txt": "delete me",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "todelete.txt")

	// Verify file exists first
	assertHostFileExists(t, filePath)

	// Unlink the file
	err := client.Unlink(filePath)
	if err != nil {
		t.Fatalf("Unlink failed: %v", err)
	}

	// Verify file is gone on host
	assertHostFileNotExists(t, filePath)
}

// ============================================================================
// Rmdir Tests
// ============================================================================

func TestP9_Rmdir_EmptyDir(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"emptydir/": "",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	subdirPath := filepath.Join(dir, "emptydir")

	// Verify directory exists first
	assertHostFileExists(t, subdirPath)

	// Remove the directory (Unlink works for directories too)
	err := client.Unlink(subdirPath)
	if err != nil {
		t.Fatalf("Unlink (rmdir) failed: %v", err)
	}

	// Verify directory is gone on host
	assertHostFileNotExists(t, subdirPath)
}

// ============================================================================
// Rename Tests
// ============================================================================

func TestP9_Rename_SameDir(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"oldname.txt": "rename me",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	oldPath := filepath.Join(dir, "oldname.txt")
	newPath := filepath.Join(dir, "newname.txt")

	// Rename the file within the same directory
	err := client.Rename(oldPath, newPath)
	if err != nil {
		t.Fatalf("Rename failed: %v", err)
	}

	// Verify old file is gone and new file exists
	assertHostFileNotExists(t, oldPath)
	assertHostFileExists(t, newPath)
	assertHostFileContent(t, newPath, []byte("rename me"))
}

func TestP9_Rename_AcrossDir(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"srcdir/moveme.txt": "moving content",
		"dstdir/":           "",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	oldPath := filepath.Join(dir, "srcdir", "moveme.txt")
	newPath := filepath.Join(dir, "dstdir", "moved.txt")

	// Move file
	err := client.Rename(oldPath, newPath)
	if err != nil {
		t.Fatalf("Rename across directories failed: %v", err)
	}

	// Verify file moved
	assertHostFileNotExists(t, oldPath)
	assertHostFileExists(t, newPath)
	assertHostFileContent(t, newPath, []byte("moving content"))
}

// ============================================================================
// Truncate Tests
// ============================================================================

func TestP9_Truncate(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "0123456789",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "testfile.txt")

	// Truncate to 5 bytes
	err := client.Truncate(filePath, 5)
	if err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	// Verify file size on host
	assertHostFileSize(t, filePath, 5)
	assertHostFileContent(t, filePath, []byte("01234"))
}

// ============================================================================
// Chmod Tests
// ============================================================================

func TestP9_Chmod(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "content",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "testfile.txt")

	// Change mode to 0600
	err := client.Chmod(filePath, 0600)
	if err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}

	// Verify file mode on host
	assertHostFileMode(t, filePath, 0600)
}

// ============================================================================
// Symlink Tests
// ============================================================================

func TestP9_Symlink_Create(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"target.txt": "target content",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	symlinkPath := filepath.Join(dir, "link.txt")

	// Create symlink
	_, err := client.Symlink(symlinkPath, "target.txt")
	if err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	// Verify symlink exists and points to target
	assertHostFileExists(t, symlinkPath)
	assertSymlinkTarget(t, symlinkPath, "target.txt")
}

func TestP9_Readlink_Target(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"target.txt": "target content",
	})

	// Create symlink on host
	symlinkPath := filepath.Join(dir, "link.txt")
	if err := os.Symlink("target.txt", symlinkPath); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Read the symlink target
	target, err := client.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("Readlink failed: %v", err)
	}

	if target != "target.txt" {
		t.Errorf("Readlink returned wrong target: got %q, want %q", target, "target.txt")
	}
}

// ============================================================================
// Fsync Tests
// ============================================================================

func TestP9_Fsync_DataPersists(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "testfile.txt")

	// Open for writing
	fid, _, err := client.Open(filePath, p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write data
	testData := []byte("fsync test data")
	_, err = client.WriteAt(fid, testData, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}

	// Fsync
	err = client.Fsync(fid)
	if err != nil {
		t.Fatalf("Fsync failed: %v", err)
	}

	// Verify data is on disk (read directly from host)
	assertHostFileContent(t, filePath, testData)

	client.CloseFID(fid)
}

// ============================================================================
// Statfs Tests
// ============================================================================

func TestP9_Statfs_Basic(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "content",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Get filesystem stats
	stats, err := client.Statfs(dir)
	if err != nil {
		t.Fatalf("Statfs failed: %v", err)
	}

	// Basic sanity checks
	if stats.Blocks == 0 {
		t.Error("Statfs returned 0 blocks")
	}
	if stats.BlockSize == 0 {
		t.Error("Statfs returned 0 block size")
	}
}

// ============================================================================
// Concurrent Operation Tests
// ============================================================================

func TestP9_Concurrent_MultipleReaders(t *testing.T) {
	content := bytes.Repeat([]byte("concurrent read test "), 1000)
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": string(content),
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "testfile.txt")

	// Start multiple concurrent readers
	const numReaders = 10
	var wg sync.WaitGroup
	errors := make(chan error, numReaders)

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			// Each reader opens the file independently
			fid, _, err := client.Open(filePath, p9.ReadOnly)
			if err != nil {
				errors <- err
				return
			}
			defer client.CloseFID(fid)

			// Read entire file
			readData, err := client.ReadAt(fid, 0, uint32(len(content)))
			if err != nil {
				errors <- err
				return
			}

			if !bytes.Equal(readData, content) {
				errors <- syscall.EIO
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent reader failed: %v", err)
	}
}

func TestP9_Concurrent_ReadersAndWriters(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Create multiple files concurrently
	const numFiles = 5
	var wg sync.WaitGroup
	errors := make(chan error, numFiles*2)

	for i := 0; i < numFiles; i++ {
		wg.Add(1)
		go func(fileID int) {
			defer wg.Done()

			fileName := "concurrent_file_" + string(byte('0'+fileID)) + ".txt"
			filePath := filepath.Join(dir, fileName)

			// Create file
			fid, _, err := client.Create(filePath, 0644, uint32(syscall.O_RDWR))
			if err != nil {
				errors <- err
				return
			}

			// Write data
			data := []byte("content for file " + string(byte('0'+fileID)))
			_, err = client.WriteAt(fid, data, 0)
			if err != nil {
				errors <- err
				client.CloseFID(fid)
				return
			}

			client.CloseFID(fid)
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent operation failed: %v", err)
	}

	// Verify all files were created
	for i := 0; i < numFiles; i++ {
		fileName := "concurrent_file_" + string(byte('0'+i)) + ".txt"
		assertHostFileExists(t, filepath.Join(dir, fileName))
	}
}

// ============================================================================
// Hard Link Tests
// ============================================================================

func TestP9_Link_Create(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"original.txt": "original content",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	originalPath := filepath.Join(dir, "original.txt")
	linkPath := filepath.Join(dir, "hardlink.txt")

	// Create hard link
	_, err := client.Link(linkPath, originalPath)
	if err != nil {
		t.Fatalf("Link failed: %v", err)
	}

	// Verify hard link exists and has same content
	assertHostFileExists(t, linkPath)
	assertHostFileContent(t, linkPath, []byte("original content"))

	// Verify both files have the same inode (hard link)
	origInfo, _ := os.Stat(originalPath)
	linkInfo, _ := os.Stat(linkPath)

	origStat := origInfo.Sys().(*syscall.Stat_t)
	linkStat := linkInfo.Sys().(*syscall.Stat_t)

	if origStat.Ino != linkStat.Ino {
		t.Errorf("Hard link has different inode: original=%d, link=%d", origStat.Ino, linkStat.Ino)
	}
}

// ============================================================================
// Edge Case Tests
// ============================================================================

func TestP9_Create_FileWithSpaces(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "file with spaces.txt")

	// Create a file with spaces
	fid, _, err := client.Create(filePath, 0644, uint32(syscall.O_RDWR))
	if err != nil {
		t.Fatalf("Create file with spaces failed: %v", err)
	}
	client.CloseFID(fid)

	assertHostFileExists(t, filePath)
}

func TestP9_ReadWrite_ZeroLength(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"empty.txt": "",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "empty.txt")

	// Open for reading
	fid, _, err := client.Open(filePath, p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer client.CloseFID(fid)

	// Read from empty file - EOF is expected for empty files
	readData, err := client.ReadAt(fid, 0, 100)
	// ReadAt returns empty slice for empty file (may return EOF error which is OK)
	if err != nil && len(readData) != 0 {
		t.Fatalf("ReadAt from empty file failed unexpectedly: %v", err)
	}

	if len(readData) != 0 {
		t.Errorf("Read from empty file returned %d bytes, expected 0", len(readData))
	}
}

func TestP9_Walk_VirtualAncestor(t *testing.T) {
	// Test that we can walk to exposed paths through virtual ancestors
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "content",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Walk should work for the exposed directory
	_, err := client.Walk(dir)
	if err != nil {
		t.Fatalf("Walk to exposed directory failed: %v", err)
	}

	// Walk should work for files inside exposed directory
	filePath := filepath.Join(dir, "testfile.txt")
	_, err = client.Walk(filePath)
	if err != nil {
		t.Fatalf("Walk to file in exposed directory failed: %v", err)
	}
}
