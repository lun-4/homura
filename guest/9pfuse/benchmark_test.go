package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hugelgupf/p9/p9"
)

// setupBenchmark creates a test environment for benchmarks.
// Returns the client, temp dir path, and cleanup function.
func setupBenchmark(b *testing.B, files map[string]string) (*P9Client, string, func()) {
	b.Helper()

	// Create temp directory with files
	dir := b.TempDir()
	for path, content := range files {
		fullPath := filepath.Join(dir, path)

		if len(path) > 0 && path[len(path)-1] == '/' {
			if err := os.MkdirAll(fullPath, 0755); err != nil {
				b.Fatalf("Failed to create directory %s: %v", fullPath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			b.Fatalf("Failed to create parent directories for %s: %v", fullPath, err)
		}

		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			b.Fatalf("Failed to create file %s: %v", fullPath, err)
		}
	}

	addr, serverCleanup := startTestServerForBench(b, []string{dir})

	client, err := NewP9Client(addr)
	if err != nil {
		serverCleanup()
		b.Fatalf("Failed to create P9 client: %v", err)
	}

	cleanup := func() {
		client.Close()
		serverCleanup()
	}

	return client, dir, cleanup
}

// startTestServerForBench starts a test server for benchmarks.
func startTestServerForBench(b *testing.B, exposedPaths []string) (addr string, cleanup func()) {
	b.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		b.Fatalf("Failed to get working directory: %v", err)
	}

	passthroughDir := filepath.Join(cwd, "..", "..", "9passthrough")
	if _, err := os.Stat(passthroughDir); os.IsNotExist(err) {
		passthroughDir = filepath.Join(cwd, "../../9passthrough")
	}

	passthroughDir, err = filepath.Abs(passthroughDir)
	if err != nil {
		b.Fatalf("Failed to get absolute path for 9passthrough: %v", err)
	}

	tmpBin := filepath.Join(b.TempDir(), "9passthrough")
	buildCmd := exec.Command("go", "build", "-o", tmpBin, ".")
	buildCmd.Dir = passthroughDir
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		b.Fatalf("Failed to build 9passthrough: %v\n%s", err, output)
	}

	ctx, cancel := context.WithCancel(context.Background())

	args := make([]string, len(exposedPaths))
	copy(args, exposedPaths)

	cmd := exec.CommandContext(ctx, tmpBin, args...)
	cmd.Env = os.Environ()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		b.Fatalf("Failed to get stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		b.Fatalf("Failed to start 9passthrough: %v", err)
	}

	portRegex := regexp.MustCompile(`9p listening on: [^:]+:(\d+)`)
	scanner := bufio.NewScanner(stderr)

	addrChan := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			if matches := portRegex.FindStringSubmatch(line); len(matches) > 1 {
				addrChan <- "127.0.0.1:" + matches[1]
				return
			}
		}
	}()

	select {
	case addr = <-addrChan:
	case <-time.After(10 * time.Second):
		cancel()
		cmd.Wait()
		b.Fatalf("Timeout waiting for 9passthrough server to start")
	}

	cleanup = func() {
		cancel()
		cmd.Wait()
	}

	return addr, cleanup
}

// =============================================================================
// Walk Benchmarks
// =============================================================================

func BenchmarkWalk_Depth1(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := client.Walk(path)
		if err != nil {
			b.Fatalf("Walk failed: %v", err)
		}
		f.Close()
	}
}

func BenchmarkWalk_Depth5(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"a/b/c/d/file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "a/b/c/d/file.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := client.Walk(path)
		if err != nil {
			b.Fatalf("Walk failed: %v", err)
		}
		f.Close()
	}
}

func BenchmarkWalk_Depth10(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"a/b/c/d/e/f/g/h/i/file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "a/b/c/d/e/f/g/h/i/file.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := client.Walk(path)
		if err != nil {
			b.Fatalf("Walk failed: %v", err)
		}
		f.Close()
	}
}

// =============================================================================
// GetAttr Benchmarks
// =============================================================================

func BenchmarkGetAttr_File(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "some content here",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := client.GetAttr(path)
		if err != nil {
			b.Fatalf("GetAttr failed: %v", err)
		}
	}
}

func BenchmarkGetAttr_Directory(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"testdir/file1.txt": "content1",
		"testdir/file2.txt": "content2",
		"testdir/file3.txt": "content3",
	})
	defer cleanup()

	path := filepath.Join(dir, "testdir")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := client.GetAttr(path)
		if err != nil {
			b.Fatalf("GetAttr failed: %v", err)
		}
	}
}

// =============================================================================
// Read Benchmarks
// =============================================================================

func benchmarkRead(b *testing.B, size int) {
	content := bytes.Repeat([]byte("x"), size)
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.bin": string(content),
	})
	defer cleanup()

	path := filepath.Join(dir, "file.bin")
	fid, _, err := client.Open(path, p9.ReadOnly)
	if err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer client.CloseFID(fid)

	b.ResetTimer()
	b.SetBytes(int64(size))

	for i := 0; i < b.N; i++ {
		data, err := client.ReadAt(fid, 0, uint32(size))
		if err != nil {
			b.Fatalf("ReadAt failed: %v", err)
		}
		if len(data) != size {
			b.Fatalf("Read size mismatch: got %d, want %d", len(data), size)
		}
	}
}

func BenchmarkRead_1KB(b *testing.B)  { benchmarkRead(b, 1024) }
func BenchmarkRead_4KB(b *testing.B)  { benchmarkRead(b, 4*1024) }
func BenchmarkRead_16KB(b *testing.B) { benchmarkRead(b, 16*1024) }
func BenchmarkRead_64KB(b *testing.B) { benchmarkRead(b, 64*1024) }

// BenchmarkRead_Sequential reads a large file in sequential chunks
func BenchmarkRead_Sequential(b *testing.B) {
	fileSize := 1024 * 1024 // 1MB
	chunkSize := 64 * 1024  // 64KB chunks

	content := bytes.Repeat([]byte("y"), fileSize)
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"large.bin": string(content),
	})
	defer cleanup()

	path := filepath.Join(dir, "large.bin")
	fid, _, err := client.Open(path, p9.ReadOnly)
	if err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer client.CloseFID(fid)

	b.ResetTimer()
	b.SetBytes(int64(fileSize))

	for i := 0; i < b.N; i++ {
		offset := uint64(0)
		totalRead := 0
		for totalRead < fileSize {
			toRead := chunkSize
			if totalRead+toRead > fileSize {
				toRead = fileSize - totalRead
			}
			data, err := client.ReadAt(fid, offset, uint32(toRead))
			if err != nil {
				b.Fatalf("ReadAt failed at offset %d: %v", offset, err)
			}
			totalRead += len(data)
			offset += uint64(len(data))
		}
	}
}

// =============================================================================
// Write Benchmarks
// =============================================================================

func benchmarkWrite(b *testing.B, size int) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{})
	defer cleanup()

	data := bytes.Repeat([]byte("w"), size)

	b.ResetTimer()
	b.SetBytes(int64(size))

	for i := 0; i < b.N; i++ {
		path := filepath.Join(dir, fmt.Sprintf("write_%d.bin", i))
		fid, _, err := client.Create(path, 0644, 0)
		if err != nil {
			b.Fatalf("Create failed: %v", err)
		}

		n, err := client.WriteAt(fid, data, 0)
		if err != nil {
			client.CloseFID(fid)
			b.Fatalf("WriteAt failed: %v", err)
		}
		if n != size {
			client.CloseFID(fid)
			b.Fatalf("Write size mismatch: got %d, want %d", n, size)
		}

		client.CloseFID(fid)
		os.Remove(path)
	}
}

func BenchmarkWrite_1KB(b *testing.B)  { benchmarkWrite(b, 1024) }
func BenchmarkWrite_4KB(b *testing.B)  { benchmarkWrite(b, 4*1024) }
func BenchmarkWrite_16KB(b *testing.B) { benchmarkWrite(b, 16*1024) }
func BenchmarkWrite_64KB(b *testing.B) { benchmarkWrite(b, 64*1024) }

// BenchmarkWrite_Sequential writes a large file in sequential chunks
func BenchmarkWrite_Sequential(b *testing.B) {
	fileSize := 1024 * 1024 // 1MB
	chunkSize := 64 * 1024  // 64KB chunks

	client, dir, cleanup := setupBenchmark(b, map[string]string{})
	defer cleanup()

	data := bytes.Repeat([]byte("s"), chunkSize)

	b.ResetTimer()
	b.SetBytes(int64(fileSize))

	for i := 0; i < b.N; i++ {
		path := filepath.Join(dir, fmt.Sprintf("seq_%d.bin", i))
		fid, _, err := client.Create(path, 0644, 0)
		if err != nil {
			b.Fatalf("Create failed: %v", err)
		}

		offset := uint64(0)
		totalWritten := 0
		for totalWritten < fileSize {
			toWrite := chunkSize
			if totalWritten+toWrite > fileSize {
				toWrite = fileSize - totalWritten
			}
			n, err := client.WriteAt(fid, data[:toWrite], offset)
			if err != nil {
				client.CloseFID(fid)
				b.Fatalf("WriteAt failed at offset %d: %v", offset, err)
			}
			totalWritten += n
			offset += uint64(n)
		}

		client.CloseFID(fid)
		os.Remove(path)
	}
}

// =============================================================================
// Open/Close Benchmarks
// =============================================================================

func BenchmarkOpenClose(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fid, _, err := client.Open(path, p9.ReadOnly)
		if err != nil {
			b.Fatalf("Open failed: %v", err)
		}
		if err := client.CloseFID(fid); err != nil {
			b.Fatalf("Close failed: %v", err)
		}
	}
}

// =============================================================================
// Readdir Benchmarks
// =============================================================================

func benchmarkReaddir(b *testing.B, numFiles int) {
	files := make(map[string]string)
	for i := 0; i < numFiles; i++ {
		files[fmt.Sprintf("testdir/file_%04d.txt", i)] = "content"
	}

	client, dir, cleanup := setupBenchmark(b, files)
	defer cleanup()

	path := filepath.Join(dir, "testdir")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries, err := client.Readdir(path)
		if err != nil {
			b.Fatalf("Readdir failed: %v", err)
		}
		if len(entries) != numFiles {
			b.Fatalf("Readdir count mismatch: got %d, want %d", len(entries), numFiles)
		}
	}
}

func BenchmarkReaddir_10(b *testing.B)  { benchmarkReaddir(b, 10) }
func BenchmarkReaddir_100(b *testing.B) { benchmarkReaddir(b, 100) }

// BenchmarkReaddir_1000 is expensive to set up - skip in short mode
func BenchmarkReaddir_1000(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping in short mode")
	}
	benchmarkReaddir(b, 1000)
}

// =============================================================================
// Create/Unlink Benchmarks
// =============================================================================

func BenchmarkCreateUnlink(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{})
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := filepath.Join(dir, fmt.Sprintf("test_%d.txt", i))
		fid, _, err := client.Create(path, 0644, 0)
		if err != nil {
			b.Fatalf("Create failed: %v", err)
		}
		client.CloseFID(fid)

		if err := client.Unlink(path); err != nil {
			os.Remove(path)
			b.Fatalf("Unlink failed: %v", err)
		}
	}
}

// =============================================================================
// Mkdir/Rmdir Benchmarks
// =============================================================================

func BenchmarkMkdirRmdir(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{})
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := filepath.Join(dir, fmt.Sprintf("testdir_%d", i))
		_, err := client.Mkdir(path, 0755)
		if err != nil {
			b.Fatalf("Mkdir failed: %v", err)
		}

		if err := client.Unlink(path); err != nil {
			os.RemoveAll(path)
			b.Fatalf("Rmdir failed: %v", err)
		}
	}
}

// =============================================================================
// Concurrent Benchmarks
// =============================================================================

func BenchmarkConcurrent_Walk(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"a/b/c/file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "a/b/c/file.txt")
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			f, err := client.Walk(path)
			if err != nil {
				b.Errorf("Walk failed: %v", err)
				return
			}
			f.Close()
		}
	})
}

func BenchmarkConcurrent_GetAttr(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, err := client.GetAttr(path)
			if err != nil {
				b.Errorf("GetAttr failed: %v", err)
				return
			}
		}
	})
}

func BenchmarkConcurrent_Read(b *testing.B) {
	content := bytes.Repeat([]byte("r"), 4096)
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.bin": string(content),
	})
	defer cleanup()

	path := filepath.Join(dir, "file.bin")
	b.SetBytes(4096)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		// Each goroutine gets its own file handle
		fid, _, err := client.Open(path, p9.ReadOnly)
		if err != nil {
			b.Errorf("Open failed: %v", err)
			return
		}
		defer client.CloseFID(fid)

		for pb.Next() {
			_, err := client.ReadAt(fid, 0, 4096)
			if err != nil {
				b.Errorf("ReadAt failed: %v", err)
				return
			}
		}
	})
}

func BenchmarkConcurrent_MixedOps(b *testing.B) {
	content := string(bytes.Repeat([]byte("r"), 1024))
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"read.txt": content,
	})
	defer cleanup()

	readPath := filepath.Join(dir, "read.txt")
	var counter int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := atomic.AddInt64(&counter, 1)

			switch n % 4 {
			case 0:
				// Walk
				f, err := client.Walk(readPath)
				if err != nil {
					b.Errorf("Walk failed: %v", err)
					return
				}
				f.Close()
			case 1:
				// GetAttr
				_, _, err := client.GetAttr(readPath)
				if err != nil {
					b.Errorf("GetAttr failed: %v", err)
					return
				}
			case 2:
				// Read
				fid, _, err := client.Open(readPath, p9.ReadOnly)
				if err != nil {
					b.Errorf("Open failed: %v", err)
					return
				}
				_, _ = client.ReadAt(fid, 0, 1024)
				client.CloseFID(fid)
			case 3:
				// Create + Delete
				tmpPath := filepath.Join(dir, fmt.Sprintf("tmp_%d.txt", n))
				fid, _, err := client.Create(tmpPath, 0644, 0)
				if err != nil {
					// May fail due to racing, that's ok
					continue
				}
				client.CloseFID(fid)
				_ = client.Unlink(tmpPath)
				os.Remove(tmpPath)
			}
		}
	})
}

// =============================================================================
// Latency Benchmarks (measures round-trip time)
// =============================================================================

func BenchmarkLatency_Walk(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "x",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")

	// Warm up
	for i := 0; i < 10; i++ {
		f, _ := client.Walk(path)
		f.Close()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := client.Walk(path)
		if err != nil {
			b.Fatalf("Walk failed: %v", err)
		}
		f.Close()
	}
}

func BenchmarkLatency_GetAttr(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "x",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")

	// Warm up
	for i := 0; i < 10; i++ {
		client.GetAttr(path)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := client.GetAttr(path)
		if err != nil {
			b.Fatalf("GetAttr failed: %v", err)
		}
	}
}

// =============================================================================
// Batch Benchmarks (multiple files in one benchmark run)
// =============================================================================

func BenchmarkBatch_GetAttr10(b *testing.B) {
	files := make(map[string]string)
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("file_%02d.txt", i)] = "content"
	}

	client, dir, cleanup := setupBenchmark(b, files)
	defer cleanup()

	paths := make([]string, 10)
	for i := 0; i < 10; i++ {
		paths[i] = filepath.Join(dir, fmt.Sprintf("file_%02d.txt", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := client.GetAttr(paths[i%10])
		if err != nil {
			b.Fatalf("GetAttr failed: %v", err)
		}
	}
}

// =============================================================================
// Memory Allocation Benchmarks
// =============================================================================

func BenchmarkAllocs_Walk(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		f, err := client.Walk(path)
		if err != nil {
			b.Fatalf("Walk failed: %v", err)
		}
		f.Close()
	}
}

func BenchmarkAllocs_Read(b *testing.B) {
	content := bytes.Repeat([]byte("a"), 4096)
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.bin": string(content),
	})
	defer cleanup()

	path := filepath.Join(dir, "file.bin")
	fid, _, err := client.Open(path, p9.ReadOnly)
	if err != nil {
		b.Fatalf("Open failed: %v", err)
	}
	defer client.CloseFID(fid)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := client.ReadAt(fid, 0, 4096)
		if err != nil {
			b.Fatalf("ReadAt failed: %v", err)
		}
	}
}

// =============================================================================
// Symlink Benchmarks
// =============================================================================

func BenchmarkSymlink(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"target.txt": "content",
	})
	defer cleanup()

	target := filepath.Join(dir, "target.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		linkPath := filepath.Join(dir, fmt.Sprintf("link_%d", i))
		_, err := client.Symlink(linkPath, target)
		if err != nil {
			b.Fatalf("Symlink failed: %v", err)
		}
		os.Remove(linkPath)
	}
}

func BenchmarkReadlink(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"target.txt": "content",
	})
	defer cleanup()

	target := filepath.Join(dir, "target.txt")
	linkPath := filepath.Join(dir, "testlink")
	if err := os.Symlink(target, linkPath); err != nil {
		b.Fatalf("Failed to create symlink: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Readlink(linkPath)
		if err != nil {
			b.Fatalf("Readlink failed: %v", err)
		}
	}
}

// =============================================================================
// Rename Benchmarks
// =============================================================================

func BenchmarkRename_SameDir(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{})
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create file
		oldPath := filepath.Join(dir, fmt.Sprintf("old_%d.txt", i))
		fid, _, err := client.Create(oldPath, 0644, 0)
		if err != nil {
			b.Fatalf("Create failed: %v", err)
		}
		client.CloseFID(fid)

		// Rename it
		newPath := filepath.Join(dir, fmt.Sprintf("new_%d.txt", i))
		if err := client.Rename(oldPath, newPath); err != nil {
			os.Remove(oldPath)
			b.Fatalf("Rename failed: %v", err)
		}

		// Cleanup
		os.Remove(newPath)
	}
}

// =============================================================================
// Truncate Benchmarks
// =============================================================================

func BenchmarkTruncate(b *testing.B) {
	content := bytes.Repeat([]byte("x"), 4096)
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": string(content),
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Alternate between truncating and expanding
		size := uint64(i%4096 + 1)
		if err := client.Truncate(path, size); err != nil {
			b.Fatalf("Truncate failed: %v", err)
		}
	}
}

// =============================================================================
// Chmod Benchmarks
// =============================================================================

func BenchmarkChmod(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mode := p9.FileMode(0644)
		if i%2 == 1 {
			mode = 0755
		}
		if err := client.Chmod(path, mode); err != nil {
			b.Fatalf("Chmod failed: %v", err)
		}
	}
}

// =============================================================================
// Statfs Benchmarks
// =============================================================================

func BenchmarkStatfs(b *testing.B) {
	client, dir, cleanup := setupBenchmark(b, map[string]string{
		"file.txt": "content",
	})
	defer cleanup()

	path := filepath.Join(dir, "file.txt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Statfs(path)
		if err != nil {
			b.Fatalf("Statfs failed: %v", err)
		}
	}
}
