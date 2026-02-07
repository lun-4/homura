package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func main() {
	dir := flag.String("dir", "/mnt/host", "Directory to test")
	concurrent := flag.Int("c", 1, "Number of concurrent operations")
	ops := flag.Int("n", 10, "Number of operations per worker")
	symlinkTest := flag.Bool("symlink", false, "Run symlink stress test")
	symlinkTmp := flag.String("symlink-tmp", "./tmp", "Temp directory for symlinks")
	flag.Parse()

	if *symlinkTest {
		runSymlinkTest(*dir, *symlinkTmp, *concurrent, *ops)
		return
	}

	fmt.Printf("Testing %s with %d concurrent workers, %d ops each\n", *dir, *concurrent, *ops)

	// First, test basic sequential access
	fmt.Println("\n=== Sequential test ===")
	start := time.Now()
	for i := 0; i < *ops; i++ {
		fmt.Printf("  [%d] stat %s... ", i, *dir)
		before := time.Now()
		info, err := os.Stat(*dir)
		elapsed := time.Since(before)
		if err != nil {
			fmt.Printf("ERROR: %v (took %v)\n", err, elapsed)
		} else {
			fmt.Printf("OK (mode=%v, took %v)\n", info.Mode(), elapsed)
		}
	}
	fmt.Printf("Sequential test done in %v\n", time.Since(start))

	// Test directory listing
	fmt.Println("\n=== Directory listing test ===")
	start = time.Now()
	entries, err := os.ReadDir(*dir)
	if err != nil {
		fmt.Printf("ReadDir failed: %v\n", err)
	} else {
		fmt.Printf("Found %d entries (took %v):\n", len(entries), time.Since(start))
		for i, e := range entries {
			if i >= 10 {
				fmt.Printf("  ... and %d more\n", len(entries)-10)
				break
			}
			fmt.Printf("  %s (dir=%v)\n", e.Name(), e.IsDir())
		}
	}

	// Test concurrent access
	if *concurrent > 1 {
		fmt.Printf("\n=== Concurrent test (%d workers) ===\n", *concurrent)
		var wg sync.WaitGroup
		start = time.Now()

		for w := 0; w < *concurrent; w++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < *ops; i++ {
					path := *dir
					if entries != nil && len(entries) > 0 {
						// Pick a random entry to stat
						path = filepath.Join(*dir, entries[i%len(entries)].Name())
					}
					before := time.Now()
					_, err := os.Stat(path)
					elapsed := time.Since(before)
					if err != nil {
						fmt.Printf("  worker %d op %d: ERROR %v (took %v)\n", id, i, err, elapsed)
					} else if elapsed > 100*time.Millisecond {
						fmt.Printf("  worker %d op %d: SLOW (took %v)\n", id, i, elapsed)
					}
				}
				fmt.Printf("  worker %d done\n", id)
			}(w)
		}

		wg.Wait()
		total := *concurrent * *ops
		elapsed := time.Since(start)
		fmt.Printf("Concurrent test done: %d ops in %v (%.0f ops/s)\n", total, elapsed, float64(total)/elapsed.Seconds())
	}

	// Test walking the tree
	fmt.Println("\n=== Walk test (first 50 files) ===")
	start = time.Now()
	count := 0
	err = filepath.WalkDir(*dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			fmt.Printf("  walk error at %s: %v\n", path, err)
			return nil // continue walking
		}
		count++
		if count > 50 {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		fmt.Printf("Walk failed: %v\n", err)
	}
	fmt.Printf("Walked %d entries in %v\n", count, time.Since(start))

	fmt.Println("\n=== Done ===")
}

func runSymlinkTest(targetDir, tmpDir string, workers, opsPerWorker int) {
	fmt.Printf("Symlink stress test: %s -> %s\n", tmpDir, targetDir)
	fmt.Printf("Workers: %d, Ops per worker: %d\n", workers, opsPerWorker)

	// Create tmp directory
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		fmt.Printf("Failed to create tmp dir: %v\n", err)
		return
	}

	// Create symlink
	symlinkPath := filepath.Join(tmpDir, "testlink")
	os.Remove(symlinkPath) // remove if exists
	if err := os.Symlink(targetDir, symlinkPath); err != nil {
		fmt.Printf("Failed to create symlink: %v\n", err)
		return
	}
	defer os.Remove(symlinkPath)

	fmt.Printf("Created symlink: %s -> %s\n\n", symlinkPath, targetDir)

	// Test 1: Sequential symlink operations
	fmt.Println("=== Test 1: Sequential symlink operations ===")
	start := time.Now()
	for i := 0; i < 20; i++ {
		before := time.Now()

		// Readlink
		target, err := os.Readlink(symlinkPath)
		if err != nil {
			fmt.Printf("  [%d] Readlink FAIL: %v\n", i, err)
			continue
		}

		// Lstat (doesn't follow)
		_, err = os.Lstat(symlinkPath)
		if err != nil {
			fmt.Printf("  [%d] Lstat FAIL: %v\n", i, err)
			continue
		}

		// Stat (follows symlink)
		info, err := os.Stat(symlinkPath)
		if err != nil {
			fmt.Printf("  [%d] Stat FAIL: %v\n", i, err)
			continue
		}

		elapsed := time.Since(before)
		fmt.Printf("  [%d] OK: target=%s, isDir=%v (took %v)\n", i, target, info.IsDir(), elapsed)
	}
	fmt.Printf("Sequential done in %v\n", time.Since(start))

	// Test 2: ReadDir through symlink
	fmt.Println("\n=== Test 2: ReadDir through symlink ===")
	start = time.Now()
	entries, err := os.ReadDir(symlinkPath)
	if err != nil {
		fmt.Printf("ReadDir FAIL: %v\n", err)
	} else {
		fmt.Printf("Found %d entries (took %v)\n", len(entries), time.Since(start))
		for i, e := range entries {
			if i >= 5 {
				fmt.Printf("  ... and %d more\n", len(entries)-5)
				break
			}
			fmt.Printf("  %s (dir=%v)\n", e.Name(), e.IsDir())
		}
	}

	// Test 3: Concurrent symlink access
	fmt.Printf("\n=== Test 3: Concurrent symlink access (%d workers, %d ops each) ===\n", workers, opsPerWorker)
	var wg sync.WaitGroup
	var success, failure int64
	var mu sync.Mutex
	start = time.Now()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			localSuccess, localFail := 0, 0

			for i := 0; i < opsPerWorker; i++ {
				// Mix of operations
				switch i % 4 {
				case 0:
					_, err := os.Readlink(symlinkPath)
					if err != nil {
						localFail++
					} else {
						localSuccess++
					}
				case 1:
					_, err := os.Lstat(symlinkPath)
					if err != nil {
						localFail++
					} else {
						localSuccess++
					}
				case 2:
					_, err := os.Stat(symlinkPath)
					if err != nil {
						localFail++
					} else {
						localSuccess++
					}
				case 3:
					_, err := os.ReadDir(symlinkPath)
					if err != nil {
						localFail++
					} else {
						localSuccess++
					}
				}
			}

			mu.Lock()
			success += int64(localSuccess)
			failure += int64(localFail)
			mu.Unlock()
			fmt.Printf("  worker %d done: %d success, %d fail\n", id, localSuccess, localFail)
		}(w)
	}

	wg.Wait()
	elapsed := time.Since(start)
	total := success + failure
	fmt.Printf("Concurrent done: %d success, %d fail in %v (%.0f ops/s)\n",
		success, failure, elapsed, float64(total)/elapsed.Seconds())

	// Test 4: Walk through symlink
	fmt.Println("\n=== Test 4: Walk through symlink (first 50 entries) ===")
	start = time.Now()
	count := 0
	err = filepath.WalkDir(symlinkPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			fmt.Printf("  walk error at %s: %v\n", path, err)
			return nil
		}
		count++
		if count > 50 {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		fmt.Printf("Walk failed: %v\n", err)
	}
	fmt.Printf("Walked %d entries in %v\n", count, time.Since(start))

	// Test 5: Nested symlink access (stat files through the symlink)
	if len(entries) > 0 {
		fmt.Println("\n=== Test 5: Nested access through symlink ===")
		start = time.Now()
		for i := 0; i < min(10, len(entries)); i++ {
			nestedPath := filepath.Join(symlinkPath, entries[i].Name())
			before := time.Now()
			info, err := os.Stat(nestedPath)
			elapsed := time.Since(before)
			if err != nil {
				fmt.Printf("  %s: FAIL %v\n", entries[i].Name(), err)
			} else {
				fmt.Printf("  %s: OK (dir=%v, took %v)\n", entries[i].Name(), info.IsDir(), elapsed)
			}
		}
		fmt.Printf("Nested access done in %v\n", time.Since(start))
	}

	fmt.Println("\n=== Symlink test complete ===")
}
