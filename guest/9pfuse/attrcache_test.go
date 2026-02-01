package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
)

// TestAttrCache_Staleness tests that the attribute cache doesn't return stale data
// after file modifications. This is the core test case for cache invalidation.
func TestAttrCache_Staleness_TruncateAfterReaddir(t *testing.T) {
	// Create a file with initial content
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "original content that is longer",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "testfile.txt")

	// Step 1: Readdir populates the cache via prefetch
	_, err := client.Readdir(dir)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Step 2: Verify cache is populated
	_, cachedAttr, ok := client.attrCache.Get(filePath)
	if !ok {
		t.Log("Cache not populated after Readdir (prefetch may be disabled)")
	} else {
		t.Logf("Cached size after Readdir: %d", cachedAttr.Size)
	}

	// Step 3: Truncate the file to 0 and write new content
	if err := client.Truncate(filePath, 0); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	// Open file for writing
	fid, _, err := client.Open(filePath, p9.WriteOnly)
	if err != nil {
		t.Fatalf("Open for write failed: %v", err)
	}

	// Write new (shorter) content
	newContent := []byte("short")
	_, err = client.WriteAt(fid, newContent, 0)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Close the file
	if err := client.CloseFID(fid); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Step 4: GetAttr should return the NEW size, not cached stale size
	_, attr, err := client.GetAttr(filePath)
	if err != nil {
		t.Fatalf("GetAttr after write failed: %v", err)
	}

	expectedSize := uint64(len(newContent))
	if attr.Size != expectedSize {
		t.Errorf("GetAttr returned stale size: got %d, want %d (cache staleness bug!)", attr.Size, expectedSize)
	}

	// Step 5: Verify by reading the actual file content
	readFid, _, err := client.Open(filePath, p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open for read failed: %v", err)
	}
	defer client.CloseFID(readFid)

	data, err := client.ReadAt(readFid, 0, 1024)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if string(data) != string(newContent) {
		t.Errorf("Read returned wrong content: got %q, want %q", string(data), string(newContent))
	}
}

// TestAttrCache_Staleness_OverwriteAfterReaddir tests overwriting an existing file
func TestAttrCache_Staleness_OverwriteAfterReaddir(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"testfile.txt": "original",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "testfile.txt")

	// Populate cache via Readdir
	_, err := client.Readdir(dir)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Truncate and write new content (simulates O_TRUNC)
	if err := client.Truncate(filePath, 0); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	fid, _, err := client.Open(filePath, p9.WriteOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	newContent := []byte("new content")
	_, err = client.WriteAt(fid, newContent, 0)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	client.CloseFID(fid)

	// Read back via 9p
	readFid, _, err := client.Open(filePath, p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open for read failed: %v", err)
	}
	defer client.CloseFID(readFid)

	data, err := client.ReadAt(readFid, 0, 1024)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if string(data) != string(newContent) {
		t.Errorf("Read content mismatch: got %q, want %q", string(data), string(newContent))
	}
}

// TestAttrCache_Staleness_CreateAfterReaddir tests that newly created files
// don't get stale cache entries from a previous Readdir
func TestAttrCache_Staleness_CreateAfterReaddir(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"existing.txt": "exists",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Populate cache via Readdir
	_, err := client.Readdir(dir)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Create a new file
	newFilePath := filepath.Join(dir, "newfile.txt")
	fid, _, err := client.Create(newFilePath, p9.FileMode(0644), 0)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	content := []byte("new file content")
	_, err = client.WriteAt(fid, content, 0)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	client.CloseFID(fid)

	// GetAttr on new file should work and return correct size
	_, attr, err := client.GetAttr(newFilePath)
	if err != nil {
		t.Fatalf("GetAttr on new file failed: %v", err)
	}

	if attr.Size != uint64(len(content)) {
		t.Errorf("New file size wrong: got %d, want %d", attr.Size, len(content))
	}
}

// TestAttrCache_Staleness_DeleteAfterReaddir tests that deleted files
// don't return stale cache data
func TestAttrCache_Staleness_DeleteAfterReaddir(t *testing.T) {
	dir := createTempFileTree(t, map[string]string{
		"todelete.txt": "will be deleted",
	})

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	filePath := filepath.Join(dir, "todelete.txt")

	// Populate cache via Readdir
	_, err := client.Readdir(dir)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Verify file exists in cache
	_, _, ok := client.attrCache.Get(filePath)
	t.Logf("File in cache before delete: %v", ok)

	// Delete the file
	if err := client.Unlink(filePath); err != nil {
		t.Fatalf("Unlink failed: %v", err)
	}

	// GetAttr should fail (file doesn't exist)
	_, _, err = client.GetAttr(filePath)
	if err == nil {
		t.Error("GetAttr succeeded for deleted file - cache staleness bug!")
	}
}

// TestAttrCache_PrefetchRace tests that prefetch followed by modifications returns correct data.
// Note: We don't use concurrent goroutines here because the p9 library can deadlock
// with highly concurrent operations on a single client.
func TestAttrCache_PrefetchRace(t *testing.T) {
	// Create a directory with many files to trigger parallel prefetch
	files := make(map[string]string)
	for i := 0; i < 20; i++ {
		files["file"+string(rune('a'+i))+".txt"] = "content"
	}
	dir := createTempFileTree(t, files)

	client, cleanup := setupP9Test(t, []string{dir})
	defer cleanup()

	// Run multiple iterations
	for iter := 0; iter < 3; iter++ {
		// Readdir triggers prefetch which populates cache
		_, err := client.Readdir(dir)
		if err != nil {
			t.Fatalf("Iteration %d: Readdir failed: %v", iter, err)
		}

		// Immediately modify a file (this should invalidate cache)
		targetFile := filepath.Join(dir, "filea.txt")
		if err := client.Truncate(targetFile, 0); err != nil {
			t.Fatalf("Iteration %d: Truncate failed: %v", iter, err)
		}

		fid, _, err := client.Open(targetFile, p9.WriteOnly)
		if err != nil {
			t.Fatalf("Iteration %d: Open failed: %v", iter, err)
		}

		newContent := []byte("modified")
		_, err = client.WriteAt(fid, newContent, 0)
		if err != nil {
			t.Fatalf("Iteration %d: Write failed: %v", iter, err)
		}
		client.CloseFID(fid)

		// GetAttr should return correct (modified) size
		_, attr, err := client.GetAttr(targetFile)
		if err != nil {
			t.Errorf("Iteration %d: GetAttr failed: %v", iter, err)
			continue
		}

		if attr.Size != uint64(len(newContent)) {
			t.Errorf("Iteration %d: Cache staleness! GetAttr returned size %d, want %d",
				iter, attr.Size, len(newContent))
		}

		// Reset file for next iteration
		os.WriteFile(targetFile, []byte("content"), 0644)
	}
}

// TestAttrCache_TTLExpiry tests that cached entries expire after TTL
func TestAttrCache_TTLExpiry(t *testing.T) {
	// Create cache with very short TTL
	cache := NewAttrCache(10 * time.Millisecond)

	path := "/test/file"
	qid := p9.QID{Path: 123}
	attr := p9.Attr{Size: 100}

	// Put entry
	cache.Put(path, qid, attr)

	// Should be present immediately
	_, _, ok := cache.Get(path)
	if !ok {
		t.Error("Entry should be present immediately after Put")
	}

	// Wait for TTL to expire
	time.Sleep(20 * time.Millisecond)

	// Should be expired now
	_, _, ok = cache.Get(path)
	if ok {
		t.Error("Entry should have expired after TTL")
	}
}

// TestAttrCache_Invalidation tests explicit cache invalidation
func TestAttrCache_Invalidation(t *testing.T) {
	cache := NewAttrCache(1 * time.Hour) // Long TTL

	path := "/test/file"
	qid := p9.QID{Path: 123}
	attr := p9.Attr{Size: 100}

	cache.Put(path, qid, attr)

	// Should be present
	_, _, ok := cache.Get(path)
	if !ok {
		t.Error("Entry should be present")
	}

	// Invalidate
	cache.Invalidate(path)

	// Should be gone
	_, _, ok = cache.Get(path)
	if ok {
		t.Error("Entry should be invalidated")
	}
}
