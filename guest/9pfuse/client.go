package main

import (
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"syscall"
	"time"

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
	// Connect to 9passthrough server with timeout
	dialer := net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	conn, err := dialer.Dial("tcp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", serverAddr, err)
	}

	// Set TCP keepalive to detect dead connections
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	// Create p9 client with larger message size for better throughput
	// 64KB allows larger reads/writes and reduces round-trips
	client, err := p9.NewClient(conn, p9.WithMessageSize(65536))
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
// Note: This is safe to call concurrently - the p9 library handles tag multiplexing
func (c *P9Client) Walk(path string) (p9.File, error) {
	// Parse path into components
	names := parsePath(path)

	// Walk from root - p9.File.Walk is thread-safe (creates a new fid via clone)
	_, file, err := c.root.Walk(names)
	if err != nil {
		return nil, fmt.Errorf("walk failed for %s: %w", path, err)
	}

	return file, nil
}

// Open opens a file for reading/writing
func (c *P9Client) Open(path string, mode p9.OpenFlags) (uint32, p9.File, error) {
	// Walk to file - no lock needed, p9 is thread-safe
	names := parsePath(path)
	_, file, err := c.root.Walk(names)
	if err != nil {
		return 0, nil, fmt.Errorf("walk failed for %s: %w", path, err)
	}

	// Open the file - no lock needed
	_, _, err = file.Open(mode)
	if err != nil {
		file.Close()
		return 0, nil, fmt.Errorf("open failed for %s: %w", path, err)
	}

	// Allocate FID and cache - only this part needs the lock
	c.mu.Lock()
	fid := c.allocateFID()
	c.files[fid] = file
	c.mu.Unlock()

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

// Readlink reads the target of a symbolic link
func (c *P9Client) Readlink(path string) (string, error) {
	// Walk to the symlink - no lock needed
	names := parsePath(path)
	_, file, err := c.root.Walk(names)
	if err != nil {
		return "", fmt.Errorf("walk failed for %s: %w", path, err)
	}
	defer file.Close()

	// Read the symlink target
	target, err := file.Readlink()
	if err != nil {
		return "", fmt.Errorf("readlink failed for %s: %w", path, err)
	}

	return target, nil
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

// Create creates a new file
func (c *P9Client) Create(path string, mode p9.FileMode, flags uint32) (uint32, p9.QID, error) {
	// Get parent directory
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	// Walk to parent - no lock needed
	names := parsePath(dir)
	_, parent, err := c.root.Walk(names)
	if err != nil {
		return 0, p9.QID{}, fmt.Errorf("walk to parent %s failed: %w", dir, err)
	}
	// NOTE: Do NOT defer parent.Close() here!
	// After Create() is called, parent BECOMES the created file handle.
	// We need to keep it open for writes.

	// Create file - no lock needed
	p9Flags := flagsToP9Create(flags)
	createdFile, qid, _, err := parent.Create(base, p9Flags, mode.Permissions(), p9.UID(0), p9.GID(0))
	if err != nil {
		parent.Close() // Close only on error
		return 0, p9.QID{}, fmt.Errorf("create %s failed: %w", path, err)
	}

	// Allocate FID and store the created file - only this part needs the lock
	c.mu.Lock()
	fid := c.allocateFID()
	c.files[fid] = createdFile
	c.mu.Unlock()

	return fid, qid, nil
}

// flagsToP9Create converts FUSE create flags to p9 OpenFlags
func flagsToP9Create(flags uint32) p9.OpenFlags {
	// Convert access mode
	accMode := flags & syscall.O_ACCMODE

	switch accMode {
	case syscall.O_RDONLY:
		return p9.ReadOnly
	case syscall.O_WRONLY:
		return p9.WriteOnly
	case syscall.O_RDWR:
		return p9.ReadWrite
	default:
		return p9.ReadWrite // Default to read-write for creation to be safe
	}
}

// Mkdir creates a new directory
func (c *P9Client) Mkdir(path string, mode p9.FileMode) (p9.QID, error) {
	// Get parent directory
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	// Walk to parent - no lock needed
	names := parsePath(dir)
	_, parent, err := c.root.Walk(names)
	if err != nil {
		return p9.QID{}, fmt.Errorf("walk to parent %s failed: %w", dir, err)
	}
	defer parent.Close()

	// Create directory
	qid, err := parent.Mkdir(base, mode.Permissions(), p9.UID(0), p9.GID(0))
	if err != nil {
		return p9.QID{}, fmt.Errorf("mkdir %s failed: %w", path, err)
	}

	return qid, nil
}

// Unlink removes a file or directory
func (c *P9Client) Unlink(path string) error {
	// Get parent directory
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	// Walk to parent - no lock needed
	names := parsePath(dir)
	_, parent, err := c.root.Walk(names)
	if err != nil {
		return fmt.Errorf("walk to parent %s failed: %w", dir, err)
	}
	defer parent.Close()

	// Unlink (removes both files and directories)
	if err := parent.UnlinkAt(base, 0); err != nil {
		return fmt.Errorf("unlink %s failed: %w", path, err)
	}

	return nil
}

// Rename renames/moves a file
func (c *P9Client) Rename(oldPath, newPath string) error {
	// Get old parent
	oldDir := filepath.Dir(oldPath)
	oldBase := filepath.Base(oldPath)

	// Get new parent
	newDir := filepath.Dir(newPath)
	newBase := filepath.Base(newPath)

	// Walk to old parent - no lock needed
	oldNames := parsePath(oldDir)
	_, oldParent, err := c.root.Walk(oldNames)
	if err != nil {
		return fmt.Errorf("walk to old parent %s failed: %w", oldDir, err)
	}
	defer oldParent.Close()

	// Walk to new parent - no lock needed
	newNames := parsePath(newDir)
	_, newParent, err := c.root.Walk(newNames)
	if err != nil {
		return fmt.Errorf("walk to new parent %s failed: %w", newDir, err)
	}
	defer newParent.Close()

	// Rename
	if err := oldParent.RenameAt(oldBase, newParent, newBase); err != nil {
		return fmt.Errorf("rename %s to %s failed: %w", oldPath, newPath, err)
	}

	return nil
}

// Truncate truncates a file to a specific size
func (c *P9Client) Truncate(path string, size uint64) error {
	file, err := c.Walk(path)
	if err != nil {
		return err
	}
	defer file.Close()

	// Use SetAttr to change size
	return file.SetAttr(p9.SetAttrMask{Size: true}, p9.SetAttr{Size: size})
}

// Chmod changes file permissions
func (c *P9Client) Chmod(path string, mode p9.FileMode) error {
	file, err := c.Walk(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return file.SetAttr(p9.SetAttrMask{Permissions: true}, p9.SetAttr{Permissions: mode.Permissions()})
}

// Chown changes file ownership
func (c *P9Client) Chown(path string, uid, gid int) error {
	file, err := c.Walk(path)
	if err != nil {
		return err
	}
	defer file.Close()

	mask := p9.SetAttrMask{}
	attr := p9.SetAttr{}

	if uid >= 0 {
		mask.UID = true
		attr.UID = p9.UID(uid)
	}
	if gid >= 0 {
		mask.GID = true
		attr.GID = p9.GID(gid)
	}

	return file.SetAttr(mask, attr)
}

// Utimens changes access and modification times
func (c *P9Client) Utimens(path string, atime, mtime time.Time) error {
	file, err := c.Walk(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return file.SetAttr(
		p9.SetAttrMask{ATime: true, ATimeNotSystemTime: true, MTime: true, MTimeNotSystemTime: true},
		p9.SetAttr{
			ATimeSeconds:     uint64(atime.Unix()),
			ATimeNanoSeconds: uint64(atime.Nanosecond()),
			MTimeSeconds:     uint64(mtime.Unix()),
			MTimeNanoSeconds: uint64(mtime.Nanosecond()),
		},
	)
}

// Symlink creates a symbolic link
func (c *P9Client) Symlink(linkPath, target string) (p9.QID, error) {
	// Get parent directory
	dir := filepath.Dir(linkPath)
	base := filepath.Base(linkPath)

	// Walk to parent - no lock needed
	names := parsePath(dir)
	_, parent, err := c.root.Walk(names)
	if err != nil {
		return p9.QID{}, fmt.Errorf("walk to parent %s failed: %w", dir, err)
	}
	defer parent.Close()

	// Create symlink
	// parent.Symlink(oldname, newname) - oldname is target, newname is link name
	qid, err := parent.Symlink(target, base, p9.UID(0), p9.GID(0))
	if err != nil {
		return p9.QID{}, fmt.Errorf("symlink %s -> %s failed: %w", linkPath, target, err)
	}

	return qid, nil
}

// Link creates a hard link
func (c *P9Client) Link(linkPath, targetPath string) (p9.QID, error) {
	// Get parent directory of link
	dir := filepath.Dir(linkPath)
	base := filepath.Base(linkPath)

	// Walk to link parent - no lock needed
	names := parsePath(dir)
	_, parent, err := c.root.Walk(names)
	if err != nil {
		return p9.QID{}, fmt.Errorf("walk to parent %s failed: %w", dir, err)
	}
	defer parent.Close()

	// Walk to target file - no lock needed
	targetNames := parsePath(targetPath)
	_, target, err := c.root.Walk(targetNames)
	if err != nil {
		return p9.QID{}, fmt.Errorf("walk to target %s failed: %w", targetPath, err)
	}
	defer target.Close()

	// Create hard link
	if err := parent.Link(target, base); err != nil {
		return p9.QID{}, fmt.Errorf("link %s -> %s failed: %w", linkPath, targetPath, err)
	}

	// Get QID of the linked file
	qid, _, _, err := target.GetAttr(p9.AttrMask{})
	if err != nil {
		return p9.QID{}, fmt.Errorf("getattr after link failed: %w", err)
	}

	return qid, nil
}

// StatfsResult contains filesystem statistics
type StatfsResult struct {
	Blocks          uint64
	BlocksFree      uint64
	BlocksAvailable uint64
	Files           uint64
	FilesFree       uint64
	BlockSize       uint64
	NameLength      uint64
}

// Statfs gets filesystem statistics
func (c *P9Client) Statfs(path string) (*StatfsResult, error) {
	file, err := c.Walk(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Call statfs on the file
	fsStat, err := file.StatFS()
	if err != nil {
		return nil, fmt.Errorf("statfs failed for %s: %w", path, err)
	}

	return &StatfsResult{
		Blocks:          fsStat.Blocks,
		BlocksFree:      fsStat.BlocksFree,
		BlocksAvailable: fsStat.BlocksAvailable,
		Files:           fsStat.Files,
		FilesFree:       fsStat.FilesFree,
		BlockSize:       uint64(fsStat.BlockSize),
		NameLength:      uint64(fsStat.NameLength),
	}, nil
}

// Fsync syncs file data to storage
func (c *P9Client) Fsync(fid uint32) error {
	file, ok := c.GetFile(fid)
	if !ok {
		return fmt.Errorf("invalid file handle: %d", fid)
	}

	// Call fsync on the file
	if err := file.FSync(); err != nil {
		return fmt.Errorf("fsync failed: %w", err)
	}

	return nil
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
