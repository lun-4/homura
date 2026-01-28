package main

import (
	"fmt"
	"net"
	"sync"

	"github.com/hugelgupf/p9/p9"
)

// P9Client wraps a p9 client connection with metadata
type P9Client struct {
	conn   net.Conn
	client *p9.Client
	root   p9.File
	mu     sync.RWMutex

	// File handle cache (fid -> file)
	files   map[uint32]p9.File
	nextFID uint32
}

// NewP9Client creates a new 9p client connection
func NewP9Client(serverAddr string) (*P9Client, error) {
	// Connect to 9passthrough server
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", serverAddr, err)
	}

	// Create p9 client
	client, err := p9.NewClient(conn, p9.WithMessageSize(8192))
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create p9 client: %w", err)
	}

	// Attach to root
	root, err := client.Attach("/")
	if err != nil {
		client.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to attach to root: %w", err)
	}

	return &P9Client{
		conn:    conn,
		client:  client,
		root:    root,
		files:   make(map[uint32]p9.File),
		nextFID: 1,
	}, nil
}

// Close closes the 9p connection
func (c *P9Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Close all open files
	for _, f := range c.files {
		_ = f.Close()
	}
	c.files = make(map[uint32]p9.File)

	return c.conn.Close()
}

// allocateFID allocates a new file ID
func (c *P9Client) allocateFID() uint32 {
	fid := c.nextFID
	c.nextFID++
	return fid
}

// Walk walks to a path and returns a file handle
func (c *P9Client) Walk(path string) (p9.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Parse path into components
	names := parsePath(path)

	// Walk from root
	_, file, err := c.root.Walk(names)
	if err != nil {
		return nil, fmt.Errorf("walk failed for %s: %w", path, err)
	}

	return file, nil
}

// Open opens a file for reading/writing
func (c *P9Client) Open(path string, mode p9.OpenFlags) (uint32, p9.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Walk to file
	names := parsePath(path)
	_, file, err := c.root.Walk(names)
	if err != nil {
		return 0, nil, fmt.Errorf("walk failed for %s: %w", path, err)
	}

	// Open the file
	_, _, err = file.Open(mode)
	if err != nil {
		file.Close()
		return 0, nil, fmt.Errorf("open failed for %s: %w", path, err)
	}

	// Allocate FID and cache
	fid := c.allocateFID()
	c.files[fid] = file

	return fid, file, nil
}

// GetFile retrieves a cached file by FID
func (c *P9Client) GetFile(fid uint32) (p9.File, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	file, ok := c.files[fid]
	return file, ok
}

// CloseFID closes and removes a file from cache
func (c *P9Client) CloseFID(fid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	file, ok := c.files[fid]
	if !ok {
		return nil
	}

	delete(c.files, fid)
	return file.Close()
}

// GetAttr gets file attributes
func (c *P9Client) GetAttr(path string) (p9.QID, p9.Attr, error) {
	file, err := c.Walk(path)
	if err != nil {
		return p9.QID{}, p9.Attr{}, err
	}
	defer file.Close()

	qid, _, attr, err := file.GetAttr(p9.AttrMask{
		Mode:  true,
		Size:  true,
		ATime: true,
		MTime: true,
		CTime: true,
	})

	return qid, attr, err
}

// ReadAt reads from a file at an offset
func (c *P9Client) ReadAt(fid uint32, offset uint64, count uint32) ([]byte, error) {
	file, ok := c.GetFile(fid)
	if !ok {
		return nil, fmt.Errorf("invalid file handle: %d", fid)
	}

	data := make([]byte, count)
	n, err := file.ReadAt(data, int64(offset))
	if err != nil && n == 0 {
		return nil, err
	}

	return data[:n], nil
}

// WriteAt writes to a file at an offset
func (c *P9Client) WriteAt(fid uint32, data []byte, offset uint64) (int, error) {
	file, ok := c.GetFile(fid)
	if !ok {
		return 0, fmt.Errorf("invalid file handle: %d", fid)
	}

	return file.WriteAt(data, int64(offset))
}

// Readdir reads directory entries
func (c *P9Client) Readdir(path string) ([]p9.Dirent, error) {
	file, err := c.Walk(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Open directory
	_, _, err = file.Open(p9.ReadOnly)
	if err != nil {
		return nil, fmt.Errorf("failed to open directory %s: %w", path, err)
	}

	// Read all entries
	var entries []p9.Dirent
	offset := uint64(0)

	for {
		dirents, err := file.Readdir(offset, 8192)
		if err != nil {
			return nil, err
		}
		if len(dirents) == 0 {
			break
		}

		entries = append(entries, dirents...)
		offset += uint64(len(dirents))
	}

	return entries, nil
}

// parsePath splits a path into components for 9p walk
func parsePath(path string) []string {
	if path == "" || path == "/" {
		return []string{}
	}

	// Remove leading/trailing slashes
	path = path[1:]
	if len(path) > 0 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}

	if path == "" {
		return []string{}
	}

	// Split on /
	parts := []string{}
	current := ""
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(path[i])
		}
	}
	if current != "" {
		parts = append(parts, current)
	}

	return parts
}
