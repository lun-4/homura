package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
)

// Test helper to start a 9p server
func startTestServer(t *testing.T, registry *PathRegistry) (net.Listener, *p9.Server, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to create listener: %v", err)
	}

	vroot := &VirtualRoot{Registry: registry}
	server := p9.NewServer(vroot)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		server.ServeContext(ctx, listener)
	}()

	cleanup := func() {
		cancel()
		listener.Close()
	}

	return listener, server, cleanup
}

// Test helper to connect a 9p client
func connectTestClient(t *testing.T, addr string) (*p9.Client, p9.File, func()) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	client, err := p9.NewClient(conn, p9.WithMessageSize(8192))
	if err != nil {
		conn.Close()
		t.Fatalf("Failed to create client: %v", err)
	}

	root, err := client.Attach("/")
	if err != nil {
		client.Close()
		conn.Close()
		t.Fatalf("Failed to attach: %v", err)
	}

	cleanup := func() {
		root.Close()
		client.Close()
		conn.Close()
	}

	return client, root, cleanup
}

func TestIntegration_EmptyMount(t *testing.T) {
	registry := NewPathRegistry()
	listener, _, serverCleanup := startTestServer(t, registry)
	defer serverCleanup()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Should be able to get attributes of root
	_, _, attr, err := root.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr on root failed: %v", err)
	}

	// Root should be a directory
	if attr.Mode&p9.ModeDirectory == 0 {
		t.Error("Root is not a directory")
	}

	// Open the root directory before reading
	_, _, err = root.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open on root failed: %v", err)
	}

	// Should be able to readdir (will be empty)
	dirents, err := root.Readdir(0, 100)
	if err != nil {
		t.Fatalf("Readdir on root failed: %v", err)
	}

	// Should be empty when no paths exposed
	if len(dirents) != 0 {
		t.Errorf("Expected empty root, got %d entries", len(dirents))
	}
}

func TestIntegration_ExposeSinglePath(t *testing.T) {
	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "9p-integration-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Setup server with exposed path
	registry := NewPathRegistry()
	if err := registry.AddPath(tmpDir, false); err != nil {
		t.Fatalf("Failed to expose path: %v", err)
	}

	listener, _, serverCleanup := startTestServer(t, registry)
	defer serverCleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Walk to the tmp directory (parent of our exposed directory)
	tmpDirBase := filepath.Base(tmpDir)
	tmpParent := filepath.Dir(tmpDir)

	// Split path into components for walking
	pathComponents := []string{}
	for tmpParent != "/" && tmpParent != "." {
		pathComponents = append([]string{filepath.Base(tmpParent)}, pathComponents...)
		tmpParent = filepath.Dir(tmpParent)
	}
	pathComponents = append(pathComponents, tmpDirBase)

	// Walk to our directory
	qids, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk to %v failed: %v", pathComponents, err)
	}
	defer dir.Close()

	if len(qids) != len(pathComponents) {
		t.Errorf("Walk returned %d QIDs, expected %d", len(qids), len(pathComponents))
	}

	// Open directory before reading
	_, _, err = dir.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open directory failed: %v", err)
	}

	// Read directory
	dirents, err := dir.Readdir(0, 100)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Should contain our test file
	foundTestFile := false
	for _, dirent := range dirents {
		if dirent.Name == "test.txt" {
			foundTestFile = true
		}
	}

	if !foundTestFile {
		t.Error("test.txt not found in directory listing")
	}

	// Walk to the test file
	_, file, err := dir.Walk([]string{"test.txt"})
	if err != nil {
		t.Fatalf("Walk to test.txt failed: %v", err)
	}
	defer file.Close()

	// Open and read the file
	_, _, err = file.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := file.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("ReadAt failed: %v", err)
	}

	content := string(buf[:n])
	if content != "hello world" {
		t.Errorf("Read wrong content: %q, expected %q", content, "hello world")
	}
}

func TestIntegration_VirtualAncestors(t *testing.T) {
	// Create nested directory structure
	tmpDir, err := os.MkdirTemp("", "9p-integration-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	nestedPath := filepath.Join(tmpDir, "a", "b", "c")
	if err := os.MkdirAll(nestedPath, 0755); err != nil {
		t.Fatalf("Failed to create nested dirs: %v", err)
	}

	testFile := filepath.Join(nestedPath, "deep.txt")
	if err := os.WriteFile(testFile, []byte("deep file"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Only expose the deep nested path
	registry := NewPathRegistry()
	if err := registry.AddPath(nestedPath, false); err != nil {
		t.Fatalf("Failed to expose path: %v", err)
	}

	listener, _, serverCleanup := startTestServer(t, registry)
	defer serverCleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Build path components
	tmpDirBase := filepath.Base(tmpDir)
	tmpParent := filepath.Dir(tmpDir)

	pathComponents := []string{}
	for tmpParent != "/" && tmpParent != "." {
		pathComponents = append([]string{filepath.Base(tmpParent)}, pathComponents...)
		tmpParent = filepath.Dir(tmpParent)
	}
	pathComponents = append(pathComponents, tmpDirBase, "a", "b", "c")

	// Should be able to walk through virtual ancestors
	_, deepDir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk to nested path failed: %v", err)
	}
	defer deepDir.Close()

	// Open and list the deep directory
	_, _, err = deepDir.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open deep directory failed: %v", err)
	}

	dirents, err := deepDir.Readdir(0, 100)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	foundFile := false
	for _, dirent := range dirents {
		if dirent.Name == "deep.txt" {
			foundFile = true
		}
	}

	if !foundFile {
		t.Error("deep.txt not found in directory listing")
	}

	// Verify parent directories are virtual (should only show exposed children)
	pathToB := pathComponents[:len(pathComponents)-1] // walk to "b"
	_, bDir, err := root.Walk(pathToB)
	if err != nil {
		t.Fatalf("Walk to b failed: %v", err)
	}
	defer bDir.Close()

	// Open before reading
	_, _, err = bDir.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open b directory failed: %v", err)
	}

	bDirents, err := bDir.Readdir(0, 100)
	if err != nil {
		t.Fatalf("Readdir on b failed: %v", err)
	}

	// Should only see "c" directory
	if len(bDirents) != 1 {
		t.Errorf("Expected 1 entry in b, got %d", len(bDirents))
	}
	if len(bDirents) > 0 && bDirents[0].Name != "c" {
		t.Errorf("Expected 'c' in b, got %q", bDirents[0].Name)
	}
}

func TestIntegration_OpenExposedDirectory(t *testing.T) {
	// Create temporary directory with files
	tmpDir, err := os.MkdirTemp("", "9p-integration-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	testFile1 := filepath.Join(tmpDir, "file1.txt")
	if err := os.WriteFile(testFile1, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	testFile2 := filepath.Join(tmpDir, "file2.txt")
	if err := os.WriteFile(testFile2, []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Setup server with exposed path
	registry := NewPathRegistry()
	if err := registry.AddPath(tmpDir, false); err != nil {
		t.Fatalf("Failed to expose path: %v", err)
	}

	listener, _, serverCleanup := startTestServer(t, registry)
	defer serverCleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Build path to walk to our directory
	tmpDirBase := filepath.Base(tmpDir)
	tmpParent := filepath.Dir(tmpDir)

	pathComponents := []string{}
	for tmpParent != "/" && tmpParent != "." {
		pathComponents = append([]string{filepath.Base(tmpParent)}, pathComponents...)
		tmpParent = filepath.Dir(tmpParent)
	}
	pathComponents = append(pathComponents, tmpDirBase)

	// Walk to the directory
	qids, dir, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk to %v failed: %v", pathComponents, err)
	}
	defer dir.Close()

	if len(qids) != len(pathComponents) {
		t.Errorf("Walk returned %d QIDs, expected %d", len(qids), len(pathComponents))
	}

	// Verify we can get attributes
	_, _, attr, err := dir.GetAttr(p9.AttrMaskAll)
	if err != nil {
		t.Fatalf("GetAttr failed: %v", err)
	}

	// Should be a directory
	if !attr.Mode.IsDir() {
		t.Errorf("Expected directory mode, got %o", attr.Mode)
	}

	// Verify mode is correctly converted (should be ~040755, not some huge number)
	if uint32(attr.Mode) > 0x1000000 {
		t.Errorf("Mode is incorrectly converted: %o (decimal: %d)", attr.Mode, attr.Mode)
	}

	// Open the directory
	qid, iounit, err := dir.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open directory failed: %v", err)
	}

	if iounit == 0 {
		t.Error("Expected non-zero iounit")
	}

	if qid.Type&p9.TypeDir == 0 {
		t.Error("QID should indicate directory")
	}

	// Read directory contents
	dirents, err := dir.Readdir(0, 100)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}

	// Should contain both test files
	foundFile1 := false
	foundFile2 := false
	for _, dirent := range dirents {
		if dirent.Name == "file1.txt" {
			foundFile1 = true
		}
		if dirent.Name == "file2.txt" {
			foundFile2 = true
		}
	}

	if !foundFile1 {
		t.Error("file1.txt not found in directory listing")
	}
	if !foundFile2 {
		t.Error("file2.txt not found in directory listing")
	}

	// Verify we found exactly 2 entries
	if len(dirents) != 2 {
		t.Errorf("Expected 2 entries in directory, got %d", len(dirents))
	}
}

func TestIntegration_ReadFileInExposedDirectory(t *testing.T) {
	// Create temporary directory with a file
	tmpDir, err := os.MkdirTemp("", "9p-integration-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test file
	testFile := filepath.Join(tmpDir, "testfile.txt")
	testContent := "Hello from 9p!"
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Setup server with exposed path
	registry := NewPathRegistry()
	if err := registry.AddPath(tmpDir, false); err != nil {
		t.Fatalf("Failed to expose path: %v", err)
	}

	listener, _, serverCleanup := startTestServer(t, registry)
	defer serverCleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Build path to the file
	tmpDirBase := filepath.Base(tmpDir)
	tmpParent := filepath.Dir(tmpDir)

	pathComponents := []string{}
	for tmpParent != "/" && tmpParent != "." {
		pathComponents = append([]string{filepath.Base(tmpParent)}, pathComponents...)
		tmpParent = filepath.Dir(tmpParent)
	}
	pathComponents = append(pathComponents, tmpDirBase, "testfile.txt")

	// Walk to the file
	_, file, err := root.Walk(pathComponents)
	if err != nil {
		t.Fatalf("Walk to file failed: %v", err)
	}
	defer file.Close()

	// Open the file
	_, _, err = file.Open(p9.ReadOnly)
	if err != nil {
		t.Fatalf("Open file failed: %v", err)
	}

	// Read the file
	buf := make([]byte, 1024)
	n, err := file.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatalf("ReadAt failed: %v", err)
	}

	content := string(buf[:n])
	if content != testContent {
		t.Errorf("Read wrong content: %q, expected %q", content, testContent)
	}
}

func TestIntegration_CreateFileInExposedDirectory(t *testing.T) {
	// Create a test directory
	tmpDir, err := os.MkdirTemp("", "9p-integration-create-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	registry := NewPathRegistry()
	if err := registry.AddPath(tmpDir, false); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	vroot := &VirtualRoot{Registry: registry}
	rootFile, err := vroot.Attach()
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	// Walk to the exposed directory
	qids, dirFile, err := rootFile.Walk(splitPath(tmpDir))
	if err != nil {
		t.Fatalf("Walk to %s failed: %v", tmpDir, err)
	}
	if len(qids) != len(splitPath(tmpDir)) {
		t.Fatalf("Walk returned wrong number of QIDs: %d, expected %d", len(qids), len(splitPath(tmpDir)))
	}

	// Try to create a new file in the directory (no uid/gid specified)
	newFileName := "newfile.txt"
	newFile, qid, _, err := dirFile.Create(newFileName, p9.WriteOnly, 0644, p9.NoUID, p9.NoGID)
	if err != nil {
		t.Fatalf("Create(%s) failed: %v", newFileName, err)
	}
	if newFile == nil {
		t.Fatalf("Create returned nil file")
	}
	if qid.Path == 0 {
		t.Errorf("Create returned zero QID")
	}

	// Write some content to the file
	testContent := []byte("test content from integration test")
	n, err := newFile.WriteAt(testContent, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}
	if n != len(testContent) {
		t.Errorf("WriteAt wrote %d bytes, expected %d", n, len(testContent))
	}

	// Close the file
	if err := newFile.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the file exists on the host filesystem
	hostPath := filepath.Join(tmpDir, newFileName)
	content, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("Failed to read created file from host: %v", err)
	}
	if string(content) != string(testContent) {
		t.Errorf("File content mismatch: %q, expected %q", content, testContent)
	}
}

func TestIntegration_CreateFileWithGID(t *testing.T) {
	// Create a test directory
	tmpDir, err := os.MkdirTemp("", "9p-integration-create-gid-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	registry := NewPathRegistry()
	if err := registry.AddPath(tmpDir, false); err != nil {
		t.Fatalf("AddPath failed: %v", err)
	}

	vroot := &VirtualRoot{Registry: registry}
	rootFile, err := vroot.Attach()
	if err != nil {
		t.Fatalf("Attach failed: %v", err)
	}

	// Walk to the exposed directory
	qids, dirFile, err := rootFile.Walk(splitPath(tmpDir))
	if err != nil {
		t.Fatalf("Walk to %s failed: %v", tmpDir, err)
	}
	if len(qids) != len(splitPath(tmpDir)) {
		t.Fatalf("Walk returned wrong number of QIDs: %d, expected %d", len(qids), len(splitPath(tmpDir)))
	}

	// Try to create a new file with gid=0 (like VM does)
	// This mimics what we see in the logs: uid=4294967295 (NoUID), gid=0
	newFileName := "newfile_withgid.txt"
	newFile, qid, _, err := dirFile.Create(newFileName, p9.ReadWrite, 0644, p9.NoUID, 0)
	if err != nil {
		t.Fatalf("Create(%s) with gid=0 failed: %v", newFileName, err)
	}
	if newFile == nil {
		t.Fatalf("Create returned nil file")
	}
	if qid.Path == 0 {
		t.Errorf("Create returned zero QID")
	}

	// Write some content to the file
	testContent := []byte("test content with gid")
	n, err := newFile.WriteAt(testContent, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %v", err)
	}
	if n != len(testContent) {
		t.Errorf("WriteAt wrote %d bytes, expected %d", n, len(testContent))
	}

	// Close the file
	if err := newFile.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the file exists on the host filesystem
	hostPath := filepath.Join(tmpDir, newFileName)
	content, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("Failed to read created file from host: %v", err)
	}
	if string(content) != string(testContent) {
		t.Errorf("File content mismatch: %q, expected %q", content, testContent)
	}
}

func TestIntegration_DynamicExpose(t *testing.T) {
	// Create two directories
	tmpDir1, err := os.MkdirTemp("", "9p-test-1-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir 1: %v", err)
	}
	defer os.RemoveAll(tmpDir1)

	tmpDir2, err := os.MkdirTemp("", "9p-test-2-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir 2: %v", err)
	}
	defer os.RemoveAll(tmpDir2)

	registry := NewPathRegistry()

	// Start with only first directory exposed
	if err := registry.AddPath(tmpDir1, false); err != nil {
		t.Fatalf("Failed to expose path: %v", err)
	}

	listener, _, serverCleanup := startTestServer(t, registry)
	defer serverCleanup()
	time.Sleep(100 * time.Millisecond)

	_, root, clientCleanup := connectTestClient(t, listener.Addr().String())
	defer clientCleanup()

	// Verify we can access tmpDir1
	tmpDir1Base := filepath.Base(tmpDir1)
	tmpParent := filepath.Dir(tmpDir1)
	pathComponents1 := []string{}
	for tmpParent != "/" && tmpParent != "." {
		pathComponents1 = append([]string{filepath.Base(tmpParent)}, pathComponents1...)
		tmpParent = filepath.Dir(tmpParent)
	}
	pathComponents1 = append(pathComponents1, tmpDir1Base)

	_, dir1, err := root.Walk(pathComponents1)
	if err != nil {
		t.Fatalf("Walk to dir1 failed: %v", err)
	}
	dir1.Close()

	// Dynamically expose second directory
	if err := registry.AddPath(tmpDir2, false); err != nil {
		t.Fatalf("Failed to dynamically expose path: %v", err)
	}

	// Verify we can now access tmpDir2
	tmpDir2Base := filepath.Base(tmpDir2)
	tmpParent = filepath.Dir(tmpDir2)
	pathComponents2 := []string{}
	for tmpParent != "/" && tmpParent != "." {
		pathComponents2 = append([]string{filepath.Base(tmpParent)}, pathComponents2...)
		tmpParent = filepath.Dir(tmpParent)
	}
	pathComponents2 = append(pathComponents2, tmpDir2Base)

	_, dir2, err := root.Walk(pathComponents2)
	if err != nil {
		t.Fatalf("Walk to dynamically exposed dir2 failed: %v", err)
	}
	dir2.Close()

	// Remove first directory
	registry.RemovePath(tmpDir1)

	// Should no longer be able to walk to dir1 (it's now hidden)
	_, _, err = root.Walk(pathComponents1)
	if err == nil {
		t.Error("Expected walk to removed path to fail, but it succeeded")
	}

	// But dir2 should still work
	_, dir2Again, err := root.Walk(pathComponents2)
	if err != nil {
		t.Fatalf("Walk to dir2 after removing dir1 failed: %v", err)
	}
	dir2Again.Close()
}
