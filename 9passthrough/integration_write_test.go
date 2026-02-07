package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
)

func TestIntegration_WriteAt_BasicWrite(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "original content",
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

	// Walk to file
	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Open for writing
	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write new content
	newContent := []byte("new content here")
	n, err := file.WriteAt(newContent, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}
	if n != len(newContent) {
		t.Errorf("Wrote %d bytes, expected %d", n, len(newContent))
	}

	// Read back through 9P to verify
	buf := make([]byte, 1024)
	n, err = file.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("ReadAt failed: %v", err)
	}
	if string(buf[:n]) != string(newContent) {
		t.Errorf("Read back %q, expected %q", buf[:n], newContent)
	}

	// Verify on host filesystem
	hostPath := filepath.Join(tempDir, "test.txt")
	assertFileContent(t, hostPath, newContent)
}

func TestIntegration_WriteAt_OffsetWrite(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "0000000000111111111122222222223333333333",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write at different offsets
	testCases := []struct {
		offset int64
		data   string
	}{
		{0, "AAA"},
		{10, "BBB"},
		{20, "CCC"},
	}

	for _, tc := range testCases {
		n, err := file.WriteAt([]byte(tc.data), tc.offset)
		if err != nil {
			t.Fatalf("WriteAt at offset %d failed: %v", tc.offset, err)
		}
		if n != len(tc.data) {
			t.Errorf("WriteAt at offset %d wrote %d bytes, expected %d", tc.offset, n, len(tc.data))
		}
	}

	// Read back and verify
	buf := make([]byte, 100)
	n, err := file.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("ReadAt failed: %v", err)
	}

	expected := "AAA0000000BBB1111111CCC22222223333333333"
	if string(buf[:n]) != expected {
		t.Errorf("Read back %q, expected %q", buf[:n], expected)
	}
}

func TestIntegration_WriteAt_AppendBeyondEOF(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "short",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write beyond EOF
	data := []byte("appended")
	offset := int64(1000)
	n, err := file.WriteAt(data, offset)
	if err != nil {
		t.Fatalf("WriteAt beyond EOF failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("Wrote %d bytes, expected %d", n, len(data))
	}

	// Verify file was extended (there will be null bytes in between)
	hostPath := filepath.Join(tempDir, "test.txt")
	info, err := os.Stat(hostPath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	expectedSize := offset + int64(len(data))
	if info.Size() != expectedSize {
		t.Errorf("File size is %d, expected %d", info.Size(), expectedSize)
	}
}

func TestIntegration_WriteAt_VirtualAncestor(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"deep/path/file.txt": "content",
	})

	registry := NewPathRegistry()
	// Only expose the deep path, making "deep" a virtual ancestor
	deepPath := filepath.Join(tempDir, "deep", "path")
	if err := registry.AddPath(deepPath, false); err != nil {
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

	// Try to open for writing - should fail
	_, _, err = virt.Open(p9.WriteOnly)
	if err == nil {
		t.Fatal("Expected error when opening virtual ancestor for writing")
	}
	// Can get EPERM (permission denied), EISDIR (is a directory), or EACCES
	// All are acceptable - just verify we got an error
}

func TestIntegration_WriteAt_MultipleWrites(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Perform multiple sequential writes
	writes := []string{"first ", "second ", "third"}
	offset := int64(0)
	for _, data := range writes {
		n, err := file.WriteAt([]byte(data), offset)
		if err != nil {
			t.Fatalf("WriteAt failed: %v", err)
		}
		offset += int64(n)
	}

	// Verify final content
	expected := []byte("first second third")
	hostPath := filepath.Join(tempDir, "test.txt")
	assertFileContent(t, hostPath, expected)
}

func TestIntegration_FSync_DataPersistence(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "original",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write data
	data := []byte("synced data")
	_, err = file.WriteAt(data, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}

	// FSync to ensure data is persisted
	err = file.FSync()
	if err != nil {
		t.Fatalf("FSync failed: %v", err)
	}

	// Verify on host filesystem
	hostPath := filepath.Join(tempDir, "test.txt")
	assertFileContent(t, hostPath, data)
}

func TestIntegration_FSync_BeforeWrite(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "content",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// FSync on newly opened file (before any writes)
	err = file.FSync()
	if err != nil {
		t.Fatalf("FSync on newly opened file failed: %v", err)
	}
}

func TestIntegration_WriteAt_LargeWrite(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"large.txt": "",
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

	pathComponents := append(splitPath(tempDir), "large.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write 10MB in 1MB chunks
	chunkSize := 1024 * 1024 // 1MB
	totalSize := 10 * chunkSize
	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = byte(i % 256)
	}

	offset := int64(0)
	for offset < int64(totalSize) {
		n, err := file.WriteAt(chunk, offset)
		if err != nil {
			t.Fatalf("WriteAt at offset %d failed: %v", offset, err)
		}
		offset += int64(n)
	}

	// Verify file size on host
	hostPath := filepath.Join(tempDir, "large.txt")
	assertFileSize(t, hostPath, int64(totalSize))
}

// SetAttr tests

func TestIntegration_SetAttr_Chmod(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "content",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Change mode to 0600
	newMode := p9.FileMode(0600)
	valid := p9.SetAttrMask{Permissions: true}
	attr := p9.SetAttr{
		Permissions: newMode,
	}

	err = file.SetAttr(valid, attr)
	if err != nil {
		t.Fatalf("SetAttr failed: %v", err)
	}

	// Verify with GetAttr
	_, _, gotAttr, err := file.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	gotMode := gotAttr.Mode.Permissions()
	if gotMode != newMode {
		t.Errorf("Mode is %o, expected %o", gotMode, newMode)
	}

	// Verify on host filesystem
	hostPath := filepath.Join(tempDir, "test.txt")
	assertFileMode(t, hostPath, 0600)
}

func TestIntegration_SetAttr_Chown(t *testing.T) {
	skipIfNotRoot(t)

	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "content",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Change ownership (requires root)
	valid := p9.SetAttrMask{UID: true, GID: true}
	attr := p9.SetAttr{
		UID: 0,
		GID: 0,
	}

	err = file.SetAttr(valid, attr)
	if err != nil {
		t.Fatalf("SetAttr failed: %v", err)
	}

	// Verify with GetAttr
	_, _, gotAttr, err := file.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	if gotAttr.UID != 0 || gotAttr.GID != 0 {
		t.Errorf("UID/GID is %d/%d, expected 0/0", gotAttr.UID, gotAttr.GID)
	}
}

func TestIntegration_SetAttr_Truncate(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "this is a long content that will be truncated",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Truncate to different sizes
	testSizes := []uint64{0, 10, 100}

	for _, size := range testSizes {
		valid := p9.SetAttrMask{Size: true}
		attr := p9.SetAttr{Size: size}

		err = file.SetAttr(valid, attr)
		if err != nil {
			t.Fatalf("SetAttr truncate to %d failed: %v", size, err)
		}

		// Verify on host filesystem
		hostPath := filepath.Join(tempDir, "test.txt")
		assertFileSize(t, hostPath, int64(size))
	}
}

func TestIntegration_SetAttr_UpdateMtime(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "content",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Set mtime to a specific time
	newTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := p9.SetAttrMask{MTime: true, MTimeNotSystemTime: true}
	attr := p9.SetAttr{
		MTimeSeconds:     uint64(newTime.Unix()),
		MTimeNanoSeconds: 0,
	}

	err = file.SetAttr(valid, attr)
	if err != nil {
		t.Fatalf("SetAttr mtime failed: %v", err)
	}

	// Verify with GetAttr
	_, _, gotAttr, err := file.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	if gotAttr.MTimeSeconds != uint64(newTime.Unix()) {
		t.Errorf("MTime is %d, expected %d", gotAttr.MTimeSeconds, newTime.Unix())
	}
}

func TestIntegration_SetAttr_UpdateAtime(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "content",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Set atime to a specific time
	newTime := time.Date(2021, 6, 15, 12, 0, 0, 0, time.UTC)
	valid := p9.SetAttrMask{ATime: true, ATimeNotSystemTime: true}
	attr := p9.SetAttr{
		ATimeSeconds:     uint64(newTime.Unix()),
		ATimeNanoSeconds: 0,
	}

	err = file.SetAttr(valid, attr)
	if err != nil {
		t.Fatalf("SetAttr atime failed: %v", err)
	}

	// Verify with GetAttr
	_, _, gotAttr, err := file.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	if gotAttr.ATimeSeconds != uint64(newTime.Unix()) {
		t.Errorf("ATime is %d, expected %d", gotAttr.ATimeSeconds, newTime.Unix())
	}
}

func TestIntegration_SetAttr_CombinedMask(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "content with some size",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	// Set mode and size together
	newMode := p9.FileMode(0755)
	newSize := uint64(10)

	valid := p9.SetAttrMask{
		Permissions: true,
		Size:        true,
	}
	attr := p9.SetAttr{
		Permissions: newMode,
		Size:        newSize,
	}

	err = file.SetAttr(valid, attr)
	if err != nil {
		t.Fatalf("SetAttr failed: %v", err)
	}

	// Verify both changes
	_, _, gotAttr, err := file.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	gotMode := gotAttr.Mode.Permissions()
	if gotMode != newMode {
		t.Errorf("Mode is %o, expected %o", gotMode, newMode)
	}

	if gotAttr.Size != newSize {
		t.Errorf("Size is %d, expected %d", gotAttr.Size, newSize)
	}

	// Verify on host filesystem
	hostPath := filepath.Join(tempDir, "test.txt")
	assertFileMode(t, hostPath, 0755)
	assertFileSize(t, hostPath, int64(newSize))
}

func TestIntegration_SetAttr_VirtualAncestor(t *testing.T) {
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

	// Try to change mode on virtual directory - should fail
	valid := p9.SetAttrMask{Permissions: true}
	attr := p9.SetAttr{Permissions: p9.FileMode(0777)}

	err = virt.SetAttr(valid, attr)
	if err == nil {
		t.Fatal("Expected error when calling SetAttr on virtual ancestor")
	}
	assertSyscallError(t, err, syscall.EPERM)
}

func TestIntegration_SetAttr_AfterWrite(t *testing.T) {
	tempDir := createTempFileTree(t, map[string]string{
		"test.txt": "original",
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

	pathComponents := append(splitPath(tempDir), "test.txt")
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	defer file.Close()

	_, _, err = file.Open(p9.ReadWrite)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Write data
	data := []byte("new data after write")
	_, err = file.WriteAt(data, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}

	// Now change the mode
	valid := p9.SetAttrMask{Permissions: true}
	attr := p9.SetAttr{Permissions: p9.FileMode(0400)}

	err = file.SetAttr(valid, attr)
	if err != nil {
		t.Fatalf("SetAttr after write failed: %v", err)
	}

	// Verify both the content and mode on host
	hostPath := filepath.Join(tempDir, "test.txt")
	assertFileContent(t, hostPath, data)
	assertFileMode(t, hostPath, 0400)
}
