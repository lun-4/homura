package main

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"
)

// MmapRegion represents a memory-mapped region of a file
type MmapRegion struct {
	ID       uint64
	FilePath string
	File     *os.File
	MmapData []byte // Host's mmap of the file
	Size     int64

	// Change detection
	Watcher *fsnotify.Watcher
	LastMod time.Time

	// Client connection for notifications
	ClientConn net.Conn

	// Synchronization
	mu sync.RWMutex
}

// MmapRegistry manages all active mmap regions
type MmapRegistry struct {
	mu      sync.RWMutex
	regions map[uint64]*MmapRegion
	nextID  uint64
}

// NewMmapRegistry creates a new mmap registry
func NewMmapRegistry() *MmapRegistry {
	return &MmapRegistry{
		regions: make(map[uint64]*MmapRegion),
		nextID:  1,
	}
}

// Register creates a new mmap region for a file
func (mr *MmapRegistry) Register(file *os.File, length int64, conn net.Conn) (*MmapRegion, error) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	// Get file info
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	filePath := file.Name()

	// Mmap the file on host
	prot := syscall.PROT_READ | syscall.PROT_WRITE
	flags := syscall.MAP_SHARED

	data, err := syscall.Mmap(int(file.Fd()), 0, int(length), prot, flags)
	if err != nil {
		return nil, fmt.Errorf("failed to mmap file %s: %w", filePath, err)
	}

	// Set up inotify watch
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		syscall.Munmap(data)
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	if err := watcher.Add(filePath); err != nil {
		watcher.Close()
		syscall.Munmap(data)
		return nil, fmt.Errorf("failed to watch file %s: %w", filePath, err)
	}

	// Create region
	region := &MmapRegion{
		ID:         mr.nextID,
		FilePath:   filePath,
		File:       file,
		MmapData:   data,
		Size:       length,
		Watcher:    watcher,
		LastMod:    info.ModTime(),
		ClientConn: conn,
	}

	mr.regions[mr.nextID] = region
	mr.nextID++

	// Start change detection goroutine
	go mr.watchForChanges(region)

	return region, nil
}

// Unregister removes and cleans up an mmap region
func (mr *MmapRegistry) Unregister(mmapID uint64) error {
	mr.mu.Lock()
	region, ok := mr.regions[mmapID]
	if !ok {
		mr.mu.Unlock()
		return fmt.Errorf("mmap region %d not found", mmapID)
	}
	delete(mr.regions, mmapID)
	mr.mu.Unlock()

	// Clean up resources
	region.mu.Lock()
	defer region.mu.Unlock()

	if region.Watcher != nil {
		region.Watcher.Close()
	}

	if region.MmapData != nil {
		if err := syscall.Munmap(region.MmapData); err != nil {
			return fmt.Errorf("failed to munmap: %w", err)
		}
		region.MmapData = nil
	}

	return nil
}

// Get retrieves an mmap region by ID
func (mr *MmapRegistry) Get(mmapID uint64) (*MmapRegion, bool) {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	region, ok := mr.regions[mmapID]
	return region, ok
}

// WriteToMmap writes data to an mmap region
func (mr *MmapRegistry) WriteToMmap(mmapID uint64, offset uint64, data []byte) error {
	region, ok := mr.Get(mmapID)
	if !ok {
		return fmt.Errorf("mmap region %d not found", mmapID)
	}

	region.mu.Lock()
	defer region.mu.Unlock()

	// Check bounds
	if offset+uint64(len(data)) > uint64(region.Size) {
		return fmt.Errorf("write out of bounds: offset=%d len=%d size=%d", offset, len(data), region.Size)
	}

	// Write to host's mmap (which writes through to file)
	copy(region.MmapData[offset:], data)

	// Force sync to disk
	if err := unix.Msync(region.MmapData, unix.MS_SYNC); err != nil {
		return fmt.Errorf("msync failed: %w", err)
	}

	// Update last modified time
	region.LastMod = time.Now()

	return nil
}

// ReadFromMmap reads data from an mmap region
func (mr *MmapRegistry) ReadFromMmap(mmapID uint64, offset uint64, length uint64) ([]byte, error) {
	region, ok := mr.Get(mmapID)
	if !ok {
		return nil, fmt.Errorf("mmap region %d not found", mmapID)
	}

	region.mu.RLock()
	defer region.mu.RUnlock()

	// Check bounds
	if offset+length > uint64(region.Size) {
		return nil, fmt.Errorf("read out of bounds: offset=%d len=%d size=%d", offset, length, region.Size)
	}

	// Copy data from mmap
	data := make([]byte, length)
	copy(data, region.MmapData[offset:offset+length])

	return data, nil
}

// watchForChanges monitors a file for external changes
func (mr *MmapRegistry) watchForChanges(region *MmapRegion) {
	for {
		select {
		case event, ok := <-region.Watcher.Events:
			if !ok {
				return // Watcher closed
			}

			if event.Op&fsnotify.Write == fsnotify.Write {
				// File was modified externally
				region.mu.Lock()

				// Re-mmap to get fresh data
				// First munmap
				if err := syscall.Munmap(region.MmapData); err != nil {
					region.mu.Unlock()
					continue
				}

				// Re-mmap
				prot := syscall.PROT_READ | syscall.PROT_WRITE
				flags := syscall.MAP_SHARED
				data, err := syscall.Mmap(int(region.File.Fd()), 0, int(region.Size), prot, flags)
				if err != nil {
					region.mu.Unlock()
					continue
				}

				region.MmapData = data
				region.LastMod = time.Now()
				region.mu.Unlock()

				// Notify guest client
				mr.notifyClient(region, 0, uint64(region.Size))
			}

		case err, ok := <-region.Watcher.Errors:
			if !ok {
				return // Watcher closed
			}
			// Log error but continue watching
			_ = err
		}
	}
}

// notifyClient sends a notification to the guest about changes
func (mr *MmapRegistry) notifyClient(region *MmapRegion, offset, length uint64) error {
	if region.ClientConn == nil {
		return nil // No client connection
	}

	// Create notification message
	msg := &MmapNotifyMessage{
		MmapID: region.ID,
		Offset: offset,
		Length: length,
	}

	// Encode and send
	data := EncodeMmapNotifyMessage(msg)
	if err := WriteMessage(region.ClientConn, MsgTypeMmapNotify, data); err != nil {
		return fmt.Errorf("failed to send notification: %w", err)
	}

	return nil
}

// List returns all active mmap regions
func (mr *MmapRegistry) List() []*MmapRegion {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	regions := make([]*MmapRegion, 0, len(mr.regions))
	for _, r := range mr.regions {
		regions = append(regions, r)
	}

	return regions
}
