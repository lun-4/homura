package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// StressTestConfig controls the stress test parameters
type StressTestConfig struct {
	ServerAddr     string
	TestDir        string // Directory on the mounted filesystem to use for testing
	NumGoroutines  int
	NumOperations  int
	FileSize       int
	Verbose        bool
}

// DefaultStressConfig returns a reasonable default config
// Override with environment variables: P9_SERVER, P9_TEST_DIR
func DefaultStressConfig() StressTestConfig {
	serverAddr := os.Getenv("P9_SERVER")
	if serverAddr == "" {
		serverAddr = "10.0.2.2:5640"
	}

	testDir := os.Getenv("P9_TEST_DIR")
	if testDir == "" {
		testDir = "/tmp/9pfuse-stress-test"
	}

	return StressTestConfig{
		ServerAddr:     serverAddr,
		TestDir:        testDir,
		NumGoroutines:  10,
		NumOperations:  100,
		FileSize:       4096,
		Verbose:        false,
	}
}

// TestConcurrentWalk tests concurrent Walk operations
func TestConcurrentWalk(t *testing.T) {
	config := DefaultStressConfig()

	// Skip if not in VM environment
	if os.Getenv("IN_VM") != "1" {
		t.Skip("Skipping: not in VM environment (set IN_VM=1 to run)")
	}

	client, err := NewP9Client(config.ServerAddr)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	var errors atomic.Int64
	var successes atomic.Int64

	// Use testDir and ancestors for walk testing
	paths := []string{"/", filepath.Dir(filepath.Dir(config.TestDir)), filepath.Dir(config.TestDir), config.TestDir}

	for i := 0; i < config.NumGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < config.NumOperations; j++ {
				path := paths[j%len(paths)]
				file, err := client.Walk(path)
				if err != nil {
					errors.Add(1)
					if config.Verbose {
						t.Logf("Goroutine %d: Walk(%s) failed: %v", id, path, err)
					}
					continue
				}
				file.Close()
				successes.Add(1)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Concurrent Walk: %d successes, %d errors", successes.Load(), errors.Load())

	if errors.Load() > 0 && successes.Load() == 0 {
		t.Errorf("All operations failed")
	}
}

// TestConcurrentReadWrite tests concurrent read/write operations on the same file
func TestConcurrentReadWrite(t *testing.T) {
	config := DefaultStressConfig()

	if os.Getenv("IN_VM") != "1" {
		t.Skip("Skipping: not in VM environment (set IN_VM=1 to run)")
	}

	client, err := NewP9Client(config.ServerAddr)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Create test directory
	testDir := config.TestDir
	_ = os.MkdirAll(testDir, 0755)

	// Create a test file
	testFile := filepath.Join(testDir, "concurrent_rw_test.dat")
	initialData := make([]byte, config.FileSize)
	rand.Read(initialData)

	if err := os.WriteFile(testFile, initialData, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	var wg sync.WaitGroup
	var readErrors atomic.Int64
	var writeErrors atomic.Int64
	var readSuccesses atomic.Int64
	var writeSuccesses atomic.Int64

	// Spawn readers
	for i := 0; i < config.NumGoroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < config.NumOperations; j++ {
				fid, _, err := client.Open(testFile, 0) // ReadOnly
				if err != nil {
					readErrors.Add(1)
					continue
				}

				data, err := client.ReadAt(fid, 0, uint32(config.FileSize))
				client.CloseFID(fid)

				if err != nil {
					readErrors.Add(1)
					continue
				}

				if len(data) == 0 {
					readErrors.Add(1)
					continue
				}

				readSuccesses.Add(1)
			}
		}(i)
	}

	// Spawn writers
	for i := 0; i < config.NumGoroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < config.NumOperations; j++ {
				fid, _, err := client.Open(testFile, 1) // WriteOnly
				if err != nil {
					writeErrors.Add(1)
					continue
				}

				data := make([]byte, 64)
				rand.Read(data)
				offset := uint64((j * 64) % config.FileSize)

				_, err = client.WriteAt(fid, data, offset)
				client.CloseFID(fid)

				if err != nil {
					writeErrors.Add(1)
					continue
				}

				writeSuccesses.Add(1)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Concurrent R/W: reads=%d/%d, writes=%d/%d",
		readSuccesses.Load(), readSuccesses.Load()+readErrors.Load(),
		writeSuccesses.Load(), writeSuccesses.Load()+writeErrors.Load())

	totalErrors := readErrors.Load() + writeErrors.Load()
	totalSuccesses := readSuccesses.Load() + writeSuccesses.Load()

	if totalErrors > 0 && totalSuccesses == 0 {
		t.Errorf("All operations failed")
	}
}

// TestConcurrentCreateDelete tests concurrent file creation and deletion
func TestConcurrentCreateDelete(t *testing.T) {
	config := DefaultStressConfig()

	if os.Getenv("IN_VM") != "1" {
		t.Skip("Skipping: not in VM environment (set IN_VM=1 to run)")
	}

	client, err := NewP9Client(config.ServerAddr)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	testDir := config.TestDir
	_ = os.MkdirAll(testDir, 0755)
	defer os.RemoveAll(testDir)

	var wg sync.WaitGroup
	var createErrors atomic.Int64
	var deleteErrors atomic.Int64
	var createSuccesses atomic.Int64
	var deleteSuccesses atomic.Int64

	// Each goroutine creates and deletes its own files
	for i := 0; i < config.NumGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < config.NumOperations; j++ {
				filename := fmt.Sprintf("test_file_%d_%d.tmp", id, j)
				filePath := filepath.Join(testDir, filename)

				// Create
				fid, _, err := client.Create(filePath, 0644, 0)
				if err != nil {
					createErrors.Add(1)
					continue
				}

				// Write some data
				data := []byte(fmt.Sprintf("data from goroutine %d iteration %d", id, j))
				_, err = client.WriteAt(fid, data, 0)
				client.CloseFID(fid)

				if err != nil {
					createErrors.Add(1)
					continue
				}
				createSuccesses.Add(1)

				// Delete
				err = client.Unlink(filePath)
				if err != nil {
					deleteErrors.Add(1)
					continue
				}
				deleteSuccesses.Add(1)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Concurrent Create/Delete: creates=%d/%d, deletes=%d/%d",
		createSuccesses.Load(), createSuccesses.Load()+createErrors.Load(),
		deleteSuccesses.Load(), deleteSuccesses.Load()+deleteErrors.Load())

	totalErrors := createErrors.Load() + deleteErrors.Load()
	totalSuccesses := createSuccesses.Load() + deleteSuccesses.Load()

	if totalErrors > 0 && totalSuccesses == 0 {
		t.Errorf("All operations failed")
	}
}

// TestConcurrentDirOperations tests concurrent directory operations
func TestConcurrentDirOperations(t *testing.T) {
	config := DefaultStressConfig()

	if os.Getenv("IN_VM") != "1" {
		t.Skip("Skipping: not in VM environment (set IN_VM=1 to run)")
	}

	client, err := NewP9Client(config.ServerAddr)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	testDir := config.TestDir
	_ = os.MkdirAll(testDir, 0755)
	defer os.RemoveAll(testDir)

	var wg sync.WaitGroup
	var errors atomic.Int64
	var successes atomic.Int64

	for i := 0; i < config.NumGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < config.NumOperations/10; j++ { // Fewer dir ops
				dirName := fmt.Sprintf("test_dir_%d_%d", id, j)
				dirPath := filepath.Join(testDir, dirName)

				// Mkdir
				_, err := client.Mkdir(dirPath, 0755)
				if err != nil {
					errors.Add(1)
					continue
				}

				// Readdir on parent
				_, err = client.Readdir(testDir)
				if err != nil {
					errors.Add(1)
					// Still try to clean up
					client.Unlink(dirPath)
					continue
				}

				// Rmdir
				err = client.Unlink(dirPath)
				if err != nil {
					errors.Add(1)
					continue
				}

				successes.Add(1)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Concurrent Dir Ops: %d successes, %d errors", successes.Load(), errors.Load())

	if errors.Load() > 0 && successes.Load() == 0 {
		t.Errorf("All operations failed")
	}
}

// TestConcurrentMixedOperations tests a mix of operations happening concurrently
func TestConcurrentMixedOperations(t *testing.T) {
	config := DefaultStressConfig()

	if os.Getenv("IN_VM") != "1" {
		t.Skip("Skipping: not in VM environment (set IN_VM=1 to run)")
	}

	client, err := NewP9Client(config.ServerAddr)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	testDir := config.TestDir
	_ = os.MkdirAll(testDir, 0755)
	defer os.RemoveAll(testDir)

	// Create some initial files
	for i := 0; i < 10; i++ {
		filePath := filepath.Join(testDir, fmt.Sprintf("initial_%d.dat", i))
		data := make([]byte, 1024)
		rand.Read(data)
		os.WriteFile(filePath, data, 0644)
	}

	var wg sync.WaitGroup
	var ops atomic.Int64
	var errors atomic.Int64

	start := time.Now()

	// Walkers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < config.NumOperations*2; j++ {
				file, err := client.Walk(testDir)
				if err != nil {
					errors.Add(1)
					continue
				}
				file.Close()
				ops.Add(1)
			}
		}()
	}

	// Readers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < config.NumOperations; j++ {
				filePath := filepath.Join(testDir, fmt.Sprintf("initial_%d.dat", j%10))
				fid, _, err := client.Open(filePath, 0)
				if err != nil {
					errors.Add(1)
					continue
				}
				client.ReadAt(fid, 0, 1024)
				client.CloseFID(fid)
				ops.Add(1)
			}
		}(i)
	}

	// GetAttr callers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < config.NumOperations*2; j++ {
				filePath := filepath.Join(testDir, fmt.Sprintf("initial_%d.dat", j%10))
				_, _, err := client.GetAttr(filePath)
				if err != nil {
					errors.Add(1)
					continue
				}
				ops.Add(1)
			}
		}()
	}

	// Readdir callers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < config.NumOperations; j++ {
				_, err := client.Readdir(testDir)
				if err != nil {
					errors.Add(1)
					continue
				}
				ops.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("Mixed Ops: %d ops in %v (%.0f ops/sec), %d errors",
		ops.Load(), elapsed, float64(ops.Load())/elapsed.Seconds(), errors.Load())

	errorRate := float64(errors.Load()) / float64(ops.Load()+errors.Load())
	if errorRate > 0.1 { // More than 10% error rate is concerning
		t.Errorf("High error rate: %.1f%%", errorRate*100)
	}
}

// TestDataIntegrity verifies that concurrent operations don't corrupt data
func TestDataIntegrity(t *testing.T) {
	config := DefaultStressConfig()

	if os.Getenv("IN_VM") != "1" {
		t.Skip("Skipping: not in VM environment (set IN_VM=1 to run)")
	}

	client, err := NewP9Client(config.ServerAddr)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	testDir := config.TestDir
	_ = os.MkdirAll(testDir, 0755)
	defer os.RemoveAll(testDir)

	// Each goroutine has its own file with known content
	numFiles := config.NumGoroutines
	expectedData := make([][]byte, numFiles)

	// Create files with unique content
	for i := 0; i < numFiles; i++ {
		data := make([]byte, config.FileSize)
		// Fill with deterministic pattern based on file ID
		for j := range data {
			data[j] = byte((i + j) % 256)
		}
		expectedData[i] = data

		filePath := filepath.Join(testDir, fmt.Sprintf("integrity_%d.dat", i))
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			t.Fatalf("Failed to create file %d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	var corruptionCount atomic.Int64
	var verifyCount atomic.Int64

	// Concurrent readers verifying data integrity
	for i := 0; i < numFiles; i++ {
		wg.Add(1)
		go func(fileID int) {
			defer wg.Done()

			filePath := filepath.Join(testDir, fmt.Sprintf("integrity_%d.dat", fileID))

			for j := 0; j < config.NumOperations; j++ {
				fid, _, err := client.Open(filePath, 0)
				if err != nil {
					continue
				}

				data, err := client.ReadAt(fid, 0, uint32(config.FileSize))
				client.CloseFID(fid)

				if err != nil {
					continue
				}

				if !bytes.Equal(data, expectedData[fileID]) {
					corruptionCount.Add(1)
					t.Logf("CORRUPTION: file %d, iteration %d, got %d bytes", fileID, j, len(data))
				} else {
					verifyCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Data Integrity: %d verifications, %d corruptions", verifyCount.Load(), corruptionCount.Load())

	if corruptionCount.Load() > 0 {
		t.Errorf("Data corruption detected: %d instances", corruptionCount.Load())
	}
}
