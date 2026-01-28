package main

import (
	"context"
	"fmt"
	"log"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/hugelgupf/p9/p9"
)

// NinePFS is the root of the FUSE filesystem
type NinePFS struct {
	fs.Inode
	client *P9Client
}

var _ fs.InodeEmbedder = (*NinePFS)(nil)

// NinePNode represents a file or directory node
type NinePNode struct {
	fs.Inode
	client *P9Client
	path   string
	fid    uint32 // File handle for open files
}

var _ fs.NodeOpener = (*NinePNode)(nil)
var _ fs.NodeReaddirer = (*NinePNode)(nil)
var _ fs.NodeGetattrer = (*NinePNode)(nil)
var _ fs.NodeLookuper = (*NinePNode)(nil)

// Lookup looks up a child entry
func (n *NinePNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := n.path + "/" + name
	if n.path == "/" {
		childPath = "/" + name
	}

	// Get attributes to check if it exists
	_, attr, err := n.client.GetAttr(childPath)
	if err != nil {
		log.Printf("Lookup failed for %s: %v", childPath, err)
		return nil, syscall.ENOENT
	}

	// Create child node
	child := &NinePNode{
		client: n.client,
		path:   childPath,
	}

	// Fill entry attributes
	fillEntryOut(out, attr)

	// Add to tree
	return n.Inode.NewInode(ctx, child, fs.StableAttr{
		Mode: modeToFileMode(attr.Mode),
		Ino:  uint64(attr.UID), // Use UID as inode number (not perfect but works)
	}), 0
}

// Getattr gets file attributes
func (n *NinePNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	_, attr, err := n.client.GetAttr(n.path)
	if err != nil {
		log.Printf("Getattr failed for %s: %v", n.path, err)
		return syscall.EIO
	}

	fillAttrOut(out, attr)
	return 0
}

// Open opens a file
func (n *NinePNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// Convert flags to p9 OpenFlags
	p9Flags := flagsToP9(flags)

	fid, _, err := n.client.Open(n.path, p9Flags)
	if err != nil {
		log.Printf("Open failed for %s: %v", n.path, err)
		return nil, 0, syscall.EIO
	}

	// Create file handle
	fh := &NinePFileHandle{
		client: n.client,
		path:   n.path,
		fid:    fid,
	}

	// Return with FOPEN_KEEP_CACHE to enable caching
	return fh, fuse.FOPEN_KEEP_CACHE, 0
}

// Readdir reads directory entries
func (n *NinePNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries, err := n.client.Readdir(n.path)
	if err != nil {
		log.Printf("Readdir failed for %s: %v", n.path, err)
		return nil, syscall.EIO
	}

	// Convert to FUSE dirents
	var dirents []fuse.DirEntry
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}

		mode := uint32(syscall.S_IFREG | 0644) // Default to regular file
		if e.Type == p9.TypeDir {
			mode = uint32(syscall.S_IFDIR | 0755)
		} else if e.Type == p9.TypeSymlink {
			mode = uint32(syscall.S_IFLNK | 0777)
		}

		dirents = append(dirents, fuse.DirEntry{
			Name: e.Name,
			Mode: mode,
			Ino:  e.Offset, // Use offset as inode
		})
	}

	return fs.NewListDirStream(dirents), 0
}

// NinePFileHandle represents an open file
type NinePFileHandle struct {
	client *P9Client
	path   string
	fid    uint32
}

var _ fs.FileReader = (*NinePFileHandle)(nil)
var _ fs.FileWriter = (*NinePFileHandle)(nil)
var _ fs.FileReleaser = (*NinePFileHandle)(nil)

// Read reads from the file
func (fh *NinePFileHandle) Read(ctx context.Context, dest []byte, offset int64) (fuse.ReadResult, syscall.Errno) {
	data, err := fh.client.ReadAt(fh.fid, uint64(offset), uint32(len(dest)))
	if err != nil {
		log.Printf("Read failed for %s at offset %d: %v", fh.path, offset, err)
		return nil, syscall.EIO
	}

	return fuse.ReadResultData(data), 0
}

// Write writes to the file
func (fh *NinePFileHandle) Write(ctx context.Context, data []byte, offset int64) (uint32, syscall.Errno) {
	n, err := fh.client.WriteAt(fh.fid, data, uint64(offset))
	if err != nil {
		log.Printf("Write failed for %s at offset %d: %v", fh.path, offset, err)
		return 0, syscall.EIO
	}

	return uint32(n), 0
}

// Release closes the file
func (fh *NinePFileHandle) Release(ctx context.Context) syscall.Errno {
	if err := fh.client.CloseFID(fh.fid); err != nil {
		log.Printf("Release failed for %s: %v", fh.path, err)
		return syscall.EIO
	}
	return 0
}

// Helper functions

func modeToFileMode(mode p9.FileMode) uint32 {
	// Convert p9 mode to Unix mode
	unixMode := uint32(mode.Permissions())

	if mode.IsDir() {
		unixMode |= syscall.S_IFDIR
	} else if mode.IsSymlink() {
		unixMode |= syscall.S_IFLNK
	} else {
		unixMode |= syscall.S_IFREG
	}

	return unixMode
}

func fillAttrOut(out *fuse.AttrOut, attr p9.Attr) {
	out.Mode = modeToFileMode(attr.Mode)
	out.Size = attr.Size
	out.Atime = uint64(attr.ATimeSeconds)
	out.Mtime = uint64(attr.MTimeSeconds)
	out.Ctime = uint64(attr.CTimeSeconds)
	out.Atimensec = uint32(attr.ATimeNanoSeconds)
	out.Mtimensec = uint32(attr.MTimeNanoSeconds)
	out.Ctimensec = uint32(attr.CTimeNanoSeconds)

	// Set cache timeout
	out.SetTimeout(1 * time.Second)
}

func fillEntryOut(out *fuse.EntryOut, attr p9.Attr) {
	out.Attr.Mode = modeToFileMode(attr.Mode)
	out.Attr.Size = attr.Size
	out.Attr.Atime = uint64(attr.ATimeSeconds)
	out.Attr.Mtime = uint64(attr.MTimeSeconds)
	out.Attr.Ctime = uint64(attr.CTimeSeconds)
	out.Attr.Atimensec = uint32(attr.ATimeNanoSeconds)
	out.Attr.Mtimensec = uint32(attr.MTimeNanoSeconds)
	out.Attr.Ctimensec = uint32(attr.CTimeNanoSeconds)

	out.SetEntryTimeout(1 * time.Second)
	out.SetAttrTimeout(1 * time.Second)
}

func flagsToP9(flags uint32) p9.OpenFlags {
	// Convert FUSE/Linux flags to p9 flags
	var p9Flags p9.OpenFlags

	accMode := flags & syscall.O_ACCMODE
	switch accMode {
	case syscall.O_RDONLY:
		p9Flags = p9.ReadOnly
	case syscall.O_WRONLY:
		p9Flags = p9.WriteOnly
	case syscall.O_RDWR:
		p9Flags = p9.ReadWrite
	}

	// TODO: Handle truncate and other flags

	return p9Flags
}

// Mount mounts the FUSE filesystem
func MountFUSE(mountpoint string, client *P9Client) (*fuse.Server, error) {
	// Create root node
	root := &NinePNode{
		client: client,
		path:   "/",
	}

	// Mount options
	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			Name:          "9pfuse",
			FsName:        "9pfuse",
			DisableXAttrs: true,
			Debug:         false,
		},
		// Enable write-through caching
		NullPermissions: true,
	}

	// Mount filesystem
	server, err := fs.Mount(mountpoint, root, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to mount FUSE filesystem: %w", err)
	}

	log.Printf("FUSE filesystem mounted at %s", mountpoint)
	return server, nil
}
