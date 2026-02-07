package main

import (
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// mockConn is a mock net.Conn for testing
type mockConn struct {
	net.Conn
	closed bool
	mu     sync.Mutex
}

func (m *mockConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockConn) Write(b []byte) (int, error) {
	return len(b), nil
}

// TestMmapRegister tests basic mmap registration
func TestMmapRegister(t *testing.T) {
	// Create test file
	tmpFile, err := os.CreateTemp("", "mmap-test-*.dat")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Write test data
	testData := []byte("Hello, mmap world!")
	if _, err := tmpFile.Write(testData); err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	size := int64(4096)
	if err := tmpFile.Truncate(size); err != nil {
		t.Fatalf("Failed to truncate file: %v", err)
	}

	// Reopen for mmap (needs read/write)
	file, err := os.OpenFile(tmpFile.Name(), os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("Failed to reopen file: %v", err)
	}
	defer file.Close()

	// Create registry and register
	registry := NewMmapRegistry()
	conn := &mockConn{}

	region, err := registry.Register(file, size, conn)
	if err != nil {
		t.Fatalf("Failed to register mmap: %v", err)
	}

	// Verify region
	if region.ID != 1 {
		t.Errorf("Expected ID=1, got %d", region.ID)
	}

	if region.Size != size {
		t.Errorf("Expected size=%d, got %d", size, region.Size)
	}

	if len(region.MmapData) != int(size) {
		t.Errorf("Expected mmap data length=%d, got %d", size, len(region.MmapData))
	}

	// Verify initial data is readable
	if string(region.MmapData[:len(testData)]) != string(testData) {
		t.Errorf("Expected data=%q, got %q", testData, region.MmapData[:len(testData)])
	}

	// Clean up
	if err := registry.Unregister(region.ID); err != nil {
		t.Errorf("Failed to unregister: %v", err)
	}
}

// TestMmapWrite tests writing to an mmap region
func TestMmapWrite(t *testing.T) {
	// Create test file
	tmpFile, err := os.CreateTemp("", "mmap-write-test-*.dat")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	size := int64(4096)

	// Open for mmap
	file, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer file.Close()

	if err := file.Truncate(size); err != nil {
		t.Fatalf("Failed to truncate: %v", err)
	}

	// Register mmap
	registry := NewMmapRegistry()
	conn := &mockConn{}

	region, err := registry.Register(file, size, conn)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	defer registry.Unregister(region.ID)

	// Write via MmapRegistry
	testData := []byte("test data from guest")
	if err := registry.WriteToMmap(region.ID, 0, testData); err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	// Verify written to mmap
	if string(region.MmapData[:len(testData)]) != string(testData) {
		t.Errorf("Data not in mmap: expected %q, got %q", testData, region.MmapData[:len(testData)])
	}

	// Verify written to file (read back from disk)
	readFile, err := os.Open(tmpPath)
	if err != nil {
		t.Fatalf("Failed to open for reading: %v", err)
	}
	defer readFile.Close()

	diskData := make([]byte, len(testData))
	if _, err := readFile.ReadAt(diskData, 0); err != nil {
		t.Fatalf("Failed to read from file: %v", err)
	}

	if string(diskData) != string(testData) {
		t.Errorf("Data not on disk: expected %q, got %q", testData, diskData)
	}
}

// TestMmapRead tests reading from an mmap region
func TestMmapRead(t *testing.T) {
	// Create test file with data
	tmpFile, err := os.CreateTemp("", "mmap-read-test-*.dat")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	testData := []byte("read this data")
	if _, err := tmpFile.Write(testData); err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	size := int64(4096)
	if err := tmpFile.Truncate(size); err != nil {
		t.Fatalf("Failed to truncate: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	// Open for mmap
	file, err := os.OpenFile(tmpPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("Failed to open: %v", err)
	}
	defer file.Close()

	// Register
	registry := NewMmapRegistry()
	conn := &mockConn{}

	region, err := registry.Register(file, size, conn)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	defer registry.Unregister(region.ID)

	// Read via MmapRegistry
	data, err := registry.ReadFromMmap(region.ID, 0, uint64(len(testData)))
	if err != nil {
		t.Fatalf("Failed to read: %v", err)
	}

	if string(data) != string(testData) {
		t.Errorf("Expected %q, got %q", testData, data)
	}
}

// TestHostChangeDetection tests inotify-based change detection
func TestHostChangeDetection(t *testing.T) {
	// Create test file
	tmpFile, err := os.CreateTemp("", "mmap-notify-test-*.dat")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	size := int64(4096)
	if err := tmpFile.Truncate(size); err != nil {
		t.Fatalf("Failed to truncate: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	// Open for mmap
	file, err := os.OpenFile(tmpPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("Failed to open: %v", err)
	}
	defer file.Close()

	// Register
	registry := NewMmapRegistry()
	conn := &mockConn{}

	region, err := registry.Register(file, size, conn)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	defer registry.Unregister(region.ID)

	// Record initial state
	initialData := make([]byte, 100)
	copy(initialData, region.MmapData[:100])

	// External write to file
	externalData := []byte("external change from host process")
	externalFile, err := os.OpenFile(tmpPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("Failed to open for external write: %v", err)
	}
	if _, err := externalFile.WriteAt(externalData, 0); err != nil {
		t.Fatalf("Failed to write externally: %v", err)
	}
	externalFile.Sync()
	externalFile.Close()

	// Wait for inotify to trigger and re-mmap
	time.Sleep(200 * time.Millisecond)

	// Verify region re-mapped with new data
	region.mu.RLock()
	newData := make([]byte, len(externalData))
	copy(newData, region.MmapData[:len(externalData)])
	region.mu.RUnlock()

	if string(newData) != string(externalData) {
		t.Errorf("Region did not re-mmap: expected %q, got %q", externalData, newData)
	}
}

// TestConcurrentWrites tests concurrent writes to mmap
func TestConcurrentWrites(t *testing.T) {
	// Create test file
	tmpFile, err := os.CreateTemp("", "mmap-concurrent-*.dat")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	size := int64(40960) // 40KB
	if err := tmpFile.Truncate(size); err != nil {
		t.Fatalf("Failed to truncate: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	// Open for mmap
	file, err := os.OpenFile(tmpPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("Failed to open: %v", err)
	}
	defer file.Close()

	// Register
	registry := NewMmapRegistry()
	conn := &mockConn{}

	region, err := registry.Register(file, size, conn)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	defer registry.Unregister(region.ID)

	// Concurrent writes
	numWriters := 10
	var wg sync.WaitGroup

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			data := []byte(fmt.Sprintf("writer-%d", n))
			offset := uint64(n * 100)

			if err := registry.WriteToMmap(region.ID, offset, data); err != nil {
				t.Errorf("Writer %d failed: %v", n, err)
			}
		}(i)
	}

	wg.Wait()

	// Verify all writes succeeded
	for i := 0; i < numWriters; i++ {
		expected := fmt.Sprintf("writer-%d", i)
		offset := i * 100

		actual := string(region.MmapData[offset : offset+len(expected)])
		if actual != expected {
			t.Errorf("Write %d failed: expected %q, got %q", i, expected, actual)
		}
	}
}

// TestMultipleRegions tests managing multiple mmap regions
func TestMultipleRegions(t *testing.T) {
	registry := NewMmapRegistry()
	conn := &mockConn{}

	var files []*os.File
	var regions []*MmapRegion

	// Create and register 5 files
	for i := 0; i < 5; i++ {
		tmpFile, err := os.CreateTemp("", fmt.Sprintf("mmap-multi-%d-*.dat", i))
		if err != nil {
			t.Fatalf("Failed to create temp file %d: %v", i, err)
		}
		defer os.Remove(tmpFile.Name())

		size := int64(4096)
		if err := tmpFile.Truncate(size); err != nil {
			t.Fatalf("Failed to truncate %d: %v", i, err)
		}

		tmpPath := tmpFile.Name()
		tmpFile.Close()

		file, err := os.OpenFile(tmpPath, os.O_RDWR, 0644)
		if err != nil {
			t.Fatalf("Failed to open %d: %v", i, err)
		}
		defer file.Close()

		region, err := registry.Register(file, size, conn)
		if err != nil {
			t.Fatalf("Failed to register %d: %v", i, err)
		}

		files = append(files, file)
		regions = append(regions, region)
	}

	// Verify all regions exist
	list := registry.List()
	if len(list) != 5 {
		t.Errorf("Expected 5 regions, got %d", len(list))
	}

	// Write unique data to each
	for i, region := range regions {
		data := []byte(fmt.Sprintf("region-%d", i))
		if err := registry.WriteToMmap(region.ID, 0, data); err != nil {
			t.Errorf("Failed to write to region %d: %v", i, err)
		}
	}

	// Verify each region has correct data
	for i, region := range regions {
		expected := fmt.Sprintf("region-%d", i)
		actual := string(region.MmapData[:len(expected)])

		if actual != expected {
			t.Errorf("Region %d: expected %q, got %q", i, expected, actual)
		}
	}

	// Unregister all
	for _, region := range regions {
		if err := registry.Unregister(region.ID); err != nil {
			t.Errorf("Failed to unregister %d: %v", region.ID, err)
		}
	}

	// Verify all cleaned up
	list = registry.List()
	if len(list) != 0 {
		t.Errorf("Expected 0 regions after cleanup, got %d", len(list))
	}
}

// TestBoundsChecking tests read/write bounds validation
func TestBoundsChecking(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "mmap-bounds-*.dat")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	size := int64(1024)
	if err := tmpFile.Truncate(size); err != nil {
		t.Fatalf("Failed to truncate: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	file, err := os.OpenFile(tmpPath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("Failed to open: %v", err)
	}
	defer file.Close()

	registry := NewMmapRegistry()
	conn := &mockConn{}

	region, err := registry.Register(file, size, conn)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	defer registry.Unregister(region.ID)

	// Test write out of bounds
	err = registry.WriteToMmap(region.ID, 1020, []byte("this is too long"))
	if err == nil {
		t.Error("Expected error for out of bounds write")
	}

	// Test read out of bounds
	_, err = registry.ReadFromMmap(region.ID, 1020, 100)
	if err == nil {
		t.Error("Expected error for out of bounds read")
	}

	// Test valid operations
	if err := registry.WriteToMmap(region.ID, 0, []byte("valid")); err != nil {
		t.Errorf("Valid write failed: %v", err)
	}

	if _, err := registry.ReadFromMmap(region.ID, 0, 100); err != nil {
		t.Errorf("Valid read failed: %v", err)
	}
}

// BenchmarkMmapWrite benchmarks write performance
func BenchmarkMmapWrite(b *testing.B) {
	tmpFile, err := os.CreateTemp("", "mmap-bench-*.dat")
	if err != nil {
		b.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	size := int64(1024 * 1024) // 1MB
	if err := tmpFile.Truncate(size); err != nil {
		b.Fatalf("Failed to truncate: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	file, err := os.OpenFile(tmpPath, os.O_RDWR, 0644)
	if err != nil {
		b.Fatalf("Failed to open: %v", err)
	}
	defer file.Close()

	registry := NewMmapRegistry()
	conn := &mockConn{}

	region, err := registry.Register(file, size, conn)
	if err != nil {
		b.Fatalf("Failed to register: %v", err)
	}
	defer registry.Unregister(region.ID)

	data := []byte("benchmark data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		offset := uint64(i%1000) * 100
		_ = registry.WriteToMmap(region.ID, offset, data)
	}
}

// BenchmarkMmapRead benchmarks read performance
func BenchmarkMmapRead(b *testing.B) {
	tmpFile, err := os.CreateTemp("", "mmap-bench-read-*.dat")
	if err != nil {
		b.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	size := int64(1024 * 1024) // 1MB
	if err := tmpFile.Truncate(size); err != nil {
		b.Fatalf("Failed to truncate: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	file, err := os.OpenFile(tmpPath, os.O_RDWR, 0644)
	if err != nil {
		b.Fatalf("Failed to open: %v", err)
	}
	defer file.Close()

	registry := NewMmapRegistry()
	conn := &mockConn{}

	region, err := registry.Register(file, size, conn)
	if err != nil {
		b.Fatalf("Failed to register: %v", err)
	}
	defer registry.Unregister(region.ID)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		offset := uint64(i%1000) * 100
		_, _ = registry.ReadFromMmap(region.ID, offset, 64)
	}
}
