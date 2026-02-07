// stress is a standalone stress test for the 9pfuse client
// Run with -dir pointing to a directory on the filesystem you want to test
// Example: 9pfuse-stress -dir /mnt/host/tmp/stress-test
package main

import (
	"bytes"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var (
	testDir       = flag.String("dir", "/mnt/host/tmp/9pfuse-stress", "Test directory on mounted filesystem")
	numGoroutines = flag.Int("goroutines", 10, "Number of concurrent goroutines")
	numOps        = flag.Int("ops", 100, "Operations per goroutine")
	fileSize      = flag.Int("size", 4096, "Size of test files in bytes")
	testType      = flag.String("test", "all", "Test type: walk, rw, create, mixed, integrity, all")
	verbose       = flag.Bool("v", false, "Verbose output")
)

func main() {
	flag.Parse()

	log.Printf("9pfuse Stress Test")
	log.Printf("  Test directory: %s", *testDir)
	log.Printf("  Goroutines: %d", *numGoroutines)
	log.Printf("  Operations per goroutine: %d", *numOps)
	log.Printf("  File size: %d bytes", *fileSize)
	log.Printf("  Test type: %s", *testType)

	// Ensure test directory exists
	if err := os.MkdirAll(*testDir, 0755); err != nil {
		log.Fatalf("Failed to create test directory: %v", err)
	}

	tests := map[string]func() error{
		"walk":      testConcurrentWalk,
		"rw":        testConcurrentReadWrite,
		"create":    testConcurrentCreateDelete,
		"mixed":     testConcurrentMixed,
		"integrity": testDataIntegrity,
	}

	var failed bool

	if *testType == "all" {
		for name, testFn := range tests {
			log.Printf("\n=== Running %s test ===", name)
			if err := testFn(); err != nil {
				log.Printf("FAILED: %s: %v", name, err)
				failed = true
			} else {
				log.Printf("PASSED: %s", name)
			}
		}
	} else if testFn, ok := tests[*testType]; ok {
		log.Printf("\n=== Running %s test ===", *testType)
		if err := testFn(); err != nil {
			log.Printf("FAILED: %s: %v", *testType, err)
			failed = true
		} else {
			log.Printf("PASSED: %s", *testType)
		}
	} else {
		log.Fatalf("Unknown test type: %s", *testType)
	}

	// Cleanup
	os.RemoveAll(*testDir)

	if failed {
		os.Exit(1)
	}
	log.Printf("\nAll tests passed!")
}

func testConcurrentWalk() error {
	var wg sync.WaitGroup
	var errors atomic.Int64
	var successes atomic.Int64

	// Use testDir and its parent for walk tests
	paths := []string{filepath.Dir(*testDir), *testDir}

	for i := 0; i < *numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < *numOps; j++ {
				path := paths[j%len(paths)]
				info, err := os.Stat(path)
				if err != nil {
					errors.Add(1)
					if *verbose {
						log.Printf("Goroutine %d: Stat(%s) failed: %v", id, path, err)
					}
					continue
				}
				_ = info
				successes.Add(1)
			}
		}(i)
	}

	wg.Wait()

	log.Printf("  Results: %d successes, %d errors", successes.Load(), errors.Load())

	if successes.Load() == 0 {
		return fmt.Errorf("all operations failed")
	}
	return nil
}

func testConcurrentReadWrite() error {
	// Create a test file
	testFile := filepath.Join(*testDir, "concurrent_rw.dat")
	initialData := make([]byte, *fileSize)
	rand.Read(initialData)

	if err := os.WriteFile(testFile, initialData, 0644); err != nil {
		return fmt.Errorf("failed to create test file: %w", err)
	}
	defer os.Remove(testFile)

	var wg sync.WaitGroup
	var readErrors atomic.Int64
	var writeErrors atomic.Int64
	var readSuccesses atomic.Int64
	var writeSuccesses atomic.Int64

	// Readers
	for i := 0; i < *numGoroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < *numOps; j++ {
				data, err := os.ReadFile(testFile)
				if err != nil {
					readErrors.Add(1)
					if *verbose {
						log.Printf("Reader %d: read failed: %v", id, err)
					}
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

	// Writers
	for i := 0; i < *numGoroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < *numOps; j++ {
				f, err := os.OpenFile(testFile, os.O_WRONLY, 0644)
				if err != nil {
					writeErrors.Add(1)
					continue
				}

				data := make([]byte, 64)
				rand.Read(data)
				offset := int64((j * 64) % *fileSize)

				_, err = f.WriteAt(data, offset)
				f.Close()

				if err != nil {
					writeErrors.Add(1)
					continue
				}
				writeSuccesses.Add(1)
			}
		}(i)
	}

	wg.Wait()

	log.Printf("  Reads: %d/%d, Writes: %d/%d",
		readSuccesses.Load(), readSuccesses.Load()+readErrors.Load(),
		writeSuccesses.Load(), writeSuccesses.Load()+writeErrors.Load())

	if readSuccesses.Load()+writeSuccesses.Load() == 0 {
		return fmt.Errorf("all operations failed")
	}
	return nil
}

func testConcurrentCreateDelete() error {
	var wg sync.WaitGroup
	var createErrors atomic.Int64
	var deleteErrors atomic.Int64
	var createSuccesses atomic.Int64
	var deleteSuccesses atomic.Int64

	for i := 0; i < *numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < *numOps; j++ {
				filename := fmt.Sprintf("file_%d_%d.tmp", id, j)
				filePath := filepath.Join(*testDir, filename)

				// Create
				f, err := os.Create(filePath)
				if err != nil {
					createErrors.Add(1)
					continue
				}

				// Write
				data := []byte(fmt.Sprintf("data from goroutine %d iteration %d", id, j))
				_, err = f.Write(data)
				f.Close()

				if err != nil {
					createErrors.Add(1)
					os.Remove(filePath)
					continue
				}
				createSuccesses.Add(1)

				// Delete
				if err := os.Remove(filePath); err != nil {
					deleteErrors.Add(1)
					continue
				}
				deleteSuccesses.Add(1)
			}
		}(i)
	}

	wg.Wait()

	log.Printf("  Creates: %d/%d, Deletes: %d/%d",
		createSuccesses.Load(), createSuccesses.Load()+createErrors.Load(),
		deleteSuccesses.Load(), deleteSuccesses.Load()+deleteErrors.Load())

	if createSuccesses.Load()+deleteSuccesses.Load() == 0 {
		return fmt.Errorf("all operations failed")
	}
	return nil
}

func testConcurrentMixed() error {
	// Create some initial files
	for i := 0; i < 10; i++ {
		filePath := filepath.Join(*testDir, fmt.Sprintf("initial_%d.dat", i))
		data := make([]byte, 1024)
		rand.Read(data)
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Errorf("failed to create initial file %d: %w", i, err)
		}
	}
	defer func() {
		for i := 0; i < 10; i++ {
			os.Remove(filepath.Join(*testDir, fmt.Sprintf("initial_%d.dat", i)))
		}
	}()

	var wg sync.WaitGroup
	var ops atomic.Int64
	var errors atomic.Int64

	start := time.Now()

	// Stat callers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < *numOps*2; j++ {
				_, err := os.Stat(*testDir)
				if err != nil {
					errors.Add(1)
					continue
				}
				ops.Add(1)
			}
		}()
	}

	// Readers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < *numOps; j++ {
				filePath := filepath.Join(*testDir, fmt.Sprintf("initial_%d.dat", j%10))
				data, err := os.ReadFile(filePath)
				if err != nil {
					errors.Add(1)
					continue
				}
				_ = data
				ops.Add(1)
			}
		}(i)
	}

	// Readdir callers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < *numOps; j++ {
				entries, err := os.ReadDir(*testDir)
				if err != nil {
					errors.Add(1)
					continue
				}
				_ = entries
				ops.Add(1)
			}
		}()
	}

	// File info callers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < *numOps*2; j++ {
				filePath := filepath.Join(*testDir, fmt.Sprintf("initial_%d.dat", j%10))
				info, err := os.Stat(filePath)
				if err != nil {
					errors.Add(1)
					continue
				}
				_ = info
				ops.Add(1)
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	log.Printf("  Operations: %d in %v (%.0f ops/sec), Errors: %d",
		ops.Load(), elapsed, float64(ops.Load())/elapsed.Seconds(), errors.Load())

	errorRate := float64(errors.Load()) / float64(ops.Load()+errors.Load())
	if errorRate > 0.1 {
		return fmt.Errorf("high error rate: %.1f%%", errorRate*100)
	}
	return nil
}

func testDataIntegrity() error {
	numFiles := *numGoroutines
	expectedData := make([][]byte, numFiles)

	// Create files with known content
	for i := 0; i < numFiles; i++ {
		data := make([]byte, *fileSize)
		for j := range data {
			data[j] = byte((i + j) % 256)
		}
		expectedData[i] = data

		filePath := filepath.Join(*testDir, fmt.Sprintf("integrity_%d.dat", i))
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return fmt.Errorf("failed to create file %d: %w", i, err)
		}
	}
	defer func() {
		for i := 0; i < numFiles; i++ {
			os.Remove(filepath.Join(*testDir, fmt.Sprintf("integrity_%d.dat", i)))
		}
	}()

	var wg sync.WaitGroup
	var corruptionCount atomic.Int64
	var verifyCount atomic.Int64

	for i := 0; i < numFiles; i++ {
		wg.Add(1)
		go func(fileID int) {
			defer wg.Done()

			filePath := filepath.Join(*testDir, fmt.Sprintf("integrity_%d.dat", fileID))

			for j := 0; j < *numOps; j++ {
				data, err := os.ReadFile(filePath)
				if err != nil {
					continue
				}

				if !bytes.Equal(data, expectedData[fileID]) {
					corruptionCount.Add(1)
					if *verbose {
						log.Printf("CORRUPTION: file %d, iteration %d", fileID, j)
					}
				} else {
					verifyCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	log.Printf("  Verifications: %d, Corruptions: %d", verifyCount.Load(), corruptionCount.Load())

	if corruptionCount.Load() > 0 {
		return fmt.Errorf("data corruption detected: %d instances", corruptionCount.Load())
	}
	return nil
}
