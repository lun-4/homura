package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	dir := flag.String("dir", "/mnt/host", "Directory to stress test")
	workers := flag.Int("workers", 10, "Number of concurrent workers")
	duration := flag.Duration("duration", 10*time.Second, "How long to run")
	flag.Parse()

	fmt.Printf("Stress testing %s with %d workers for %v\n", *dir, *workers, *duration)

	var ops atomic.Int64
	var errors atomic.Int64
	done := make(chan struct{})

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			worker(id, *dir, &ops, &errors, done)
		}(i)
	}

	// Progress reporter
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(start).Seconds()
				o := ops.Load()
				e := errors.Load()
				fmt.Printf("[%.0fs] ops: %d (%.0f/s), errors: %d\n", elapsed, o, float64(o)/elapsed, e)
			}
		}
	}()

	// Run for duration
	time.Sleep(*duration)
	close(done)
	wg.Wait()

	total := ops.Load()
	errs := errors.Load()
	fmt.Printf("\nDone! Total ops: %d, errors: %d, rate: %.0f ops/s\n",
		total, errs, float64(total)/duration.Seconds())
}

func worker(id int, dir string, ops, errors *atomic.Int64, done chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}

		// Do a mix of operations
		switch ops.Load() % 4 {
		case 0:
			// Stat the root
			if _, err := os.Stat(dir); err != nil {
				errors.Add(1)
				fmt.Printf("worker %d: stat %s failed: %v\n", id, dir, err)
			}
			ops.Add(1)

		case 1:
			// List directory
			entries, err := os.ReadDir(dir)
			if err != nil {
				errors.Add(1)
				fmt.Printf("worker %d: readdir %s failed: %v\n", id, dir, err)
			} else {
				ops.Add(1)
				// Stat first few entries
				for i, e := range entries {
					if i >= 3 {
						break
					}
					select {
					case <-done:
						return
					default:
					}
					path := filepath.Join(dir, e.Name())
					if _, err := os.Stat(path); err != nil {
						errors.Add(1)
					} else {
						ops.Add(1)
					}
				}
			}

		case 2:
			// Walk a bit of the tree
			count := 0
			filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				select {
				case <-done:
					return filepath.SkipAll
				default:
				}
				if err != nil {
					errors.Add(1)
					return nil
				}
				ops.Add(1)
				count++
				if count > 20 {
					return filepath.SkipAll
				}
				return nil
			})

		case 3:
			// Try to read a small file if one exists
			entries, err := os.ReadDir(dir)
			if err == nil {
				for _, e := range entries {
					if !e.IsDir() {
						info, err := e.Info()
						if err == nil && info.Size() < 10000 {
							path := filepath.Join(dir, e.Name())
							if data, err := os.ReadFile(path); err != nil {
								errors.Add(1)
							} else {
								_ = data
								ops.Add(1)
							}
							break
						}
					}
				}
			}
			ops.Add(1)
		}
	}
}
