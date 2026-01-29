package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: test-fs <directory>")
		os.Exit(1)
	}

	testDir := os.Args[1]
	fmt.Printf("Testing filesystem operations in: %s\n\n", testDir)

	tests := []struct {
		name string
		fn   func(string) error
	}{
		{"Basic file write", testBasicWrite},
		{"Binary file write", testBinaryWrite},
		{"Large file write", testLargeWrite},
		{"Sequential writes", testSequentialWrites},
		{"Write with O_WRONLY", testWriteOnly},
		{"Write with O_RDWR", testReadWrite},
		{"Write with O_TRUNC", testTruncate},
		{"Fsync after write", testFsync},
		{"Atomic write (temp + rename)", testAtomicWrite},
		{"Multiple small writes", testMultipleSmallWrites},
		{"Write at offset", testWriteAtOffset},
		{"Overwrite existing", testOverwrite},
		{"Low-level syscall write", testLowLevel},
	}

	passed := 0
	failed := 0

	for _, test := range tests {
		fmt.Printf("Testing: %s... ", test.name)
		if err := test.fn(testDir); err != nil {
			fmt.Printf("❌ FAILED: %v\n", err)
			failed++
		} else {
			fmt.Printf("✅ PASSED\n")
			passed++
		}
	}

	fmt.Printf("\n========================================\n")
	fmt.Printf("Results: %d passed, %d failed\n", passed, failed)

	if failed > 0 {
		os.Exit(1)
	}
}

func testBasicWrite(dir string) error {
	path := filepath.Join(dir, "test_basic.txt")
	defer os.Remove(path)

	data := []byte("Hello, World!")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("WriteFile failed: %w", err)
	}

	readData, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ReadFile failed: %w", err)
	}

	if !bytes.Equal(data, readData) {
		return fmt.Errorf("data mismatch: wrote %q, read %q", data, readData)
	}

	return nil
}

func testBinaryWrite(dir string) error {
	path := filepath.Join(dir, "test_binary.beam")
	defer os.Remove(path)

	// Simulate BEAM file header
	data := []byte{0x46, 0x4F, 0x52, 0x31, 0x00, 0x00, 0x10, 0x00}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Create failed: %w", err)
	}
	defer f.Close()

	n, err := f.Write(data)
	if err != nil {
		return fmt.Errorf("Write failed: %w", err)
	}
	if n != len(data) {
		return fmt.Errorf("short write: wrote %d bytes, expected %d", n, len(data))
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("Close failed: %w", err)
	}

	readData, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ReadFile failed: %w", err)
	}

	if !bytes.Equal(data, readData) {
		return fmt.Errorf("binary data mismatch")
	}

	return nil
}

func testLargeWrite(dir string) error {
	path := filepath.Join(dir, "test_large.dat")
	defer os.Remove(path)

	// Write 1MB
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("WriteFile failed: %w", err)
	}

	stat, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("Stat failed: %w", err)
	}

	if stat.Size() != int64(len(data)) {
		return fmt.Errorf("size mismatch: got %d, expected %d", stat.Size(), len(data))
	}

	return nil
}

func testSequentialWrites(dir string) error {
	path := filepath.Join(dir, "test_sequential.txt")
	defer os.Remove(path)

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Create failed: %w", err)
	}
	defer f.Close()

	for i := 0; i < 100; i++ {
		line := fmt.Sprintf("Line %d\n", i)
		if _, err := f.WriteString(line); err != nil {
			return fmt.Errorf("WriteString failed at line %d: %w", i, err)
		}
	}

	return nil
}

func testWriteOnly(dir string) error {
	path := filepath.Join(dir, "test_wronly.txt")
	defer os.Remove(path)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("OpenFile with O_WRONLY failed: %w", err)
	}
	defer f.Close()

	data := []byte("written with O_WRONLY")
	n, err := f.Write(data)
	if err != nil {
		return fmt.Errorf("Write failed: %w", err)
	}
	if n != len(data) {
		return fmt.Errorf("short write: %d != %d", n, len(data))
	}

	return nil
}

func testReadWrite(dir string) error {
	path := filepath.Join(dir, "test_rdwr.txt")
	defer os.Remove(path)

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("OpenFile with O_RDWR failed: %w", err)
	}
	defer f.Close()

	data := []byte("written with O_RDWR")
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("Write failed: %w", err)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("Seek failed: %w", err)
	}

	readData := make([]byte, len(data))
	if _, err := f.Read(readData); err != nil {
		return fmt.Errorf("Read failed: %w", err)
	}

	if !bytes.Equal(data, readData) {
		return fmt.Errorf("data mismatch")
	}

	return nil
}

func testTruncate(dir string) error {
	path := filepath.Join(dir, "test_trunc.txt")
	defer os.Remove(path)

	// Create file with initial data
	if err := os.WriteFile(path, []byte("initial data"), 0644); err != nil {
		return fmt.Errorf("initial WriteFile failed: %w", err)
	}

	// Open with O_TRUNC
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("OpenFile with O_TRUNC failed: %w", err)
	}
	defer f.Close()

	newData := []byte("truncated")
	if _, err := f.Write(newData); err != nil {
		return fmt.Errorf("Write after truncate failed: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("Close failed: %w", err)
	}

	readData, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ReadFile failed: %w", err)
	}

	if !bytes.Equal(newData, readData) {
		return fmt.Errorf("truncate failed: expected %q, got %q", newData, readData)
	}

	return nil
}

func testFsync(dir string) error {
	path := filepath.Join(dir, "test_fsync.txt")
	defer os.Remove(path)

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Create failed: %w", err)
	}
	defer f.Close()

	data := []byte("data to sync")
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("Write failed: %w", err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("Sync failed: %w", err)
	}

	return nil
}

func testAtomicWrite(dir string) error {
	finalPath := filepath.Join(dir, "test_final.txt")
	tempPath := filepath.Join(dir, "test_temp.txt.tmp")
	defer os.Remove(finalPath)
	defer os.Remove(tempPath)

	// Write to temp file
	data := []byte("atomic write")
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("WriteFile to temp failed: %w", err)
	}

	// Rename to final location
	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("Rename failed: %w", err)
	}

	// Verify
	readData, err := os.ReadFile(finalPath)
	if err != nil {
		return fmt.Errorf("ReadFile failed: %w", err)
	}

	if !bytes.Equal(data, readData) {
		return fmt.Errorf("data mismatch after rename")
	}

	return nil
}

func testMultipleSmallWrites(dir string) error {
	path := filepath.Join(dir, "test_multiple.txt")
	defer os.Remove(path)

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Create failed: %w", err)
	}
	defer f.Close()

	// Write many small chunks (similar to Elixir compiler)
	for i := 0; i < 1000; i++ {
		chunk := []byte{byte(i % 256)}
		if _, err := f.Write(chunk); err != nil {
			return fmt.Errorf("Write %d failed: %w", i, err)
		}
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("Sync failed: %w", err)
	}

	return nil
}

func testWriteAtOffset(dir string) error {
	path := filepath.Join(dir, "test_offset.txt")
	defer os.Remove(path)

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Create failed: %w", err)
	}
	defer f.Close()

	// Write at various offsets
	if _, err := f.WriteAt([]byte("END"), 100); err != nil {
		return fmt.Errorf("WriteAt 100 failed: %w", err)
	}

	if _, err := f.WriteAt([]byte("START"), 0); err != nil {
		return fmt.Errorf("WriteAt 0 failed: %w", err)
	}

	if _, err := f.WriteAt([]byte("MID"), 50); err != nil {
		return fmt.Errorf("WriteAt 50 failed: %w", err)
	}

	return nil
}

func testOverwrite(dir string) error {
	path := filepath.Join(dir, "test_overwrite.txt")
	defer os.Remove(path)

	// Create initial file
	if err := os.WriteFile(path, []byte("AAAAAAAAAA"), 0644); err != nil {
		return fmt.Errorf("initial write failed: %w", err)
	}

	// Overwrite with different data
	if err := os.WriteFile(path, []byte("BBBB"), 0644); err != nil {
		return fmt.Errorf("overwrite failed: %w", err)
	}

	// Verify
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ReadFile failed: %w", err)
	}

	if string(data) != "BBBB" {
		return fmt.Errorf("overwrite verification failed: got %q", data)
	}

	return nil
}

func testLowLevel(dir string) error {
	path := filepath.Join(dir, "test_lowlevel.txt")
	defer os.Remove(path)

	// Open using syscall
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_WRONLY|syscall.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("syscall.Open failed: %w", err)
	}
	defer syscall.Close(fd)

	data := []byte("low level write")
	n, err := syscall.Write(fd, data)
	if err != nil {
		return fmt.Errorf("syscall.Write failed: %w", err)
	}
	if n != len(data) {
		return fmt.Errorf("short write: %d != %d", n, len(data))
	}

	return nil
}
