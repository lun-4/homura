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
var _ fs.NodeReadlinker = (*NinePNode)(nil)
var _ fs.NodeCreater = (*NinePNode)(nil)
var _ fs.NodeMkdirer = (*NinePNode)(nil)
var _ fs.NodeUnlinker = (*NinePNode)(nil)
var _ fs.NodeRmdirer = (*NinePNode)(nil)
var _ fs.NodeRenamer = (*NinePNode)(nil)
var _ fs.NodeSetattrer = (*NinePNode)(nil)
var _ fs.NodeSymlinker = (*NinePNode)(nil)
var _ fs.NodeLinker = (*NinePNode)(nil)
var _ fs.NodeStatfser = (*NinePNode)(nil)

// Lookup looks up a child entry
func (n *NinePNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	span := StartSpan("fuse.Lookup")
	defer span.End()

	childPath := n.path + "/" + name
	if n.path == "/" {
		childPath = "/" + name
	}

	log.Printf("Lookup: looking up %s", childPath)

	// Get attributes to check if it exists
	_, attr, err := n.client.GetAttr(childPath)
	if err != nil {
		log.Printf("Lookup FAILED for %s: %v", childPath, err)
		return nil, syscall.ENOENT
	}

	log.Printf("Lookup SUCCESS for %s", childPath)

	// Create child node
	child := &NinePNode{
		client: n.client,
		path:   childPath,
	}

	// Fill entry attributes
	fillEntryOut(out, attr)

	// Add to tree - use 0 to let FUSE generate stable inodes
	// (using host inode causes conflicts after rename)
	return n.Inode.NewInode(ctx, child, fs.StableAttr{
		Mode: modeToFileMode(attr.Mode),
		Ino:  0,
	}), 0
}

// Getattr gets file attributes
func (n *NinePNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	span := StartSpan("fuse.Getattr")
	defer span.End()

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
	// Invalidate cache if opening for write (file may be modified)
	if flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) != 0 {
		n.client.attrCache.Invalidate(n.path)
	}

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

	// Only use FOPEN_KEEP_CACHE for read-only opens
	// Write opens must not keep cache or kernel will serve stale data
	var fuseFlags uint32
	if flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_TRUNC|syscall.O_APPEND) == 0 {
		fuseFlags = fuse.FOPEN_KEEP_CACHE
	}
	return fh, fuseFlags, 0
}

// Readlink reads the target of a symbolic link
func (n *NinePNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	target, err := n.client.Readlink(n.path)
	if err != nil {
		log.Printf("Readlink failed for %s: %v", n.path, err)
		return nil, syscall.EIO
	}
	return []byte(target), 0
}

// Create creates a new file
func (n *NinePNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	childPath := n.path + "/" + name
	if n.path == "/" {
		childPath = "/" + name
	}

	// Invalidate parent dir cache (new entry added)
	n.client.attrCache.Invalidate(n.path)

	// Create the file via 9p
	fid, _, err := n.client.Create(childPath, p9.FileMode(mode), flags)
	if err != nil {
		log.Printf("Create failed for %s: %v", childPath, err)
		return nil, nil, 0, syscall.EIO
	}

	// Create child node
	child := &NinePNode{
		client: n.client,
		path:   childPath,
	}

	// Create file handle
	fh := &NinePFileHandle{
		client: n.client,
		path:   childPath,
		fid:    fid,
	}

	// Fill entry attributes (use defaults for newly created file)
	out.Attr.Mode = mode
	out.Attr.Size = 0
	out.SetEntryTimeout(0)
	out.SetAttrTimeout(0)

	// Don't use FOPEN_KEEP_CACHE for newly created files (will be written to)
	return n.Inode.NewInode(ctx, child, fs.StableAttr{
		Mode: mode,
		Ino:  0,
	}), fh, 0, 0
}

// Mkdir creates a new directory
func (n *NinePNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := n.path + "/" + name
	if n.path == "/" {
		childPath = "/" + name
	}

	// Create directory via 9p
	_, err := n.client.Mkdir(childPath, p9.FileMode(mode|syscall.S_IFDIR))
	if err != nil {
		log.Printf("Mkdir failed for %s: %v", childPath, err)
		return nil, syscall.EIO
	}

	// Create child node
	child := &NinePNode{
		client: n.client,
		path:   childPath,
	}

	// Fill entry attributes
	out.Attr.Mode = mode | syscall.S_IFDIR
	out.Attr.Size = 0
	out.SetEntryTimeout(0)
	out.SetAttrTimeout(0)

	return n.Inode.NewInode(ctx, child, fs.StableAttr{
		Mode: mode | syscall.S_IFDIR,
		Ino:  0,
	}), 0
}

// Unlink removes a file
func (n *NinePNode) Unlink(ctx context.Context, name string) syscall.Errno {
	childPath := n.path + "/" + name
	if n.path == "/" {
		childPath = "/" + name
	}

	// Invalidate cache entries
	n.client.attrCache.Invalidate(childPath)
	n.client.attrCache.Invalidate(n.path)

	if err := n.client.Unlink(childPath); err != nil {
		log.Printf("Unlink failed for %s: %v", childPath, err)
		return syscall.EIO
	}

	return 0
}

// Rmdir removes a directory
func (n *NinePNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	childPath := n.path + "/" + name
	if n.path == "/" {
		childPath = "/" + name
	}

	// Invalidate cache entries
	n.client.attrCache.Invalidate(childPath)
	n.client.attrCache.Invalidate(n.path)

	if err := n.client.Unlink(childPath); err != nil {
		log.Printf("Rmdir failed for %s: %v", childPath, err)
		return syscall.EIO
	}

	return 0
}

// Rename renames/moves a file or directory
func (n *NinePNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	oldPath := n.path + "/" + name
	if n.path == "/" {
		oldPath = "/" + name
	}

	newParentNode, ok := newParent.(*NinePNode)
	if !ok {
		log.Printf("Rename: newParent is not *NinePNode")
		return syscall.EIO
	}

	newPath := newParentNode.path + "/" + newName
	if newParentNode.path == "/" {
		newPath = "/" + newName
	}

	log.Printf("Rename: %s -> %s", oldPath, newPath)

	if err := n.client.Rename(oldPath, newPath); err != nil {
		log.Printf("Rename failed from %s to %s: %v", oldPath, newPath, err)
		return syscall.EIO
	}

	log.Printf("Rename succeeded: %s -> %s", oldPath, newPath)
	return 0
}

// Setattr changes file attributes
func (n *NinePNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	// Invalidate cache - attributes are being modified
	n.client.attrCache.Invalidate(n.path)

	// Handle truncate
	if in.Valid&fuse.FATTR_SIZE != 0 {
		if err := n.client.Truncate(n.path, in.Size); err != nil {
			log.Printf("Truncate failed for %s: %v", n.path, err)
			return syscall.EIO
		}
	}

	// Handle chmod
	if in.Valid&fuse.FATTR_MODE != 0 {
		if err := n.client.Chmod(n.path, p9.FileMode(in.Mode)); err != nil {
			log.Printf("Chmod failed for %s: %v", n.path, err)
			return syscall.EIO
		}
	}

	// Handle chown (UID/GID)
	if (in.Valid&fuse.FATTR_UID != 0) || (in.Valid&fuse.FATTR_GID != 0) {
		uid := int(in.Uid)
		gid := int(in.Gid)
		if err := n.client.Chown(n.path, uid, gid); err != nil {
			log.Printf("Chown failed for %s: %v", n.path, err)
			// Don't fail on chown errors - many filesystems ignore this
		}
	}

	// Handle utimens (modify/access times)
	if (in.Valid&fuse.FATTR_MTIME != 0) || (in.Valid&fuse.FATTR_ATIME != 0) {
		atime := time.Unix(int64(in.Atime), int64(in.Atimensec))
		mtime := time.Unix(int64(in.Mtime), int64(in.Mtimensec))
		if err := n.client.Utimens(n.path, atime, mtime); err != nil {
			log.Printf("Utimens failed for %s: %v", n.path, err)
			// Don't fail on utimens errors
		}
	}

	// Refresh attributes and return them
	return n.Getattr(ctx, f, out)
}

// Symlink creates a symbolic link
func (n *NinePNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	linkPath := n.path + "/" + name
	if n.path == "/" {
		linkPath = "/" + name
	}

	// Create symlink via 9p
	_, err := n.client.Symlink(linkPath, target)
	if err != nil {
		log.Printf("Symlink failed for %s -> %s: %v", linkPath, target, err)
		return nil, syscall.EIO
	}

	// Create child node
	child := &NinePNode{
		client: n.client,
		path:   linkPath,
	}

	// Fill entry attributes
	out.Attr.Mode = syscall.S_IFLNK | 0777
	out.Attr.Size = uint64(len(target))
	out.SetEntryTimeout(0)
	out.SetAttrTimeout(0)

	return n.Inode.NewInode(ctx, child, fs.StableAttr{
		Mode: syscall.S_IFLNK | 0777,
		Ino:  0,
	}), 0
}

// Link creates a hard link
func (n *NinePNode) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	targetNode, ok := target.(*NinePNode)
	if !ok {
		return nil, syscall.EIO
	}

	linkPath := n.path + "/" + name
	if n.path == "/" {
		linkPath = "/" + name
	}

	// Create hard link via 9p
	_, err := n.client.Link(linkPath, targetNode.path)
	if err != nil {
		log.Printf("Link failed for %s -> %s: %v", linkPath, targetNode.path, err)
		return nil, syscall.EIO
	}

	// Create child node (same inode as target)
	child := &NinePNode{
		client: n.client,
		path:   linkPath,
	}

	// Get target attributes
	_, attr, err := n.client.GetAttr(targetNode.path)
	if err != nil {
		return nil, syscall.EIO
	}

	// Fill entry attributes
	fillEntryOut(out, attr)

	return n.Inode.NewInode(ctx, child, fs.StableAttr{
		Mode: modeToFileMode(attr.Mode),
		Ino:  0,
	}), 0
}

// Statfs returns filesystem statistics
func (n *NinePNode) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	// Get filesystem stats via 9p
	stats, err := n.client.Statfs(n.path)
	if err != nil {
		log.Printf("Statfs failed for %s: %v", n.path, err)
		return syscall.EIO
	}

	out.Blocks = stats.Blocks
	out.Bfree = stats.BlocksFree
	out.Bavail = stats.BlocksAvailable
	out.Files = stats.Files
	out.Ffree = stats.FilesFree
	out.Bsize = uint32(stats.BlockSize)
	out.NameLen = uint32(stats.NameLength)
	out.Frsize = uint32(stats.BlockSize)

	return 0
}

// Readdir reads directory entries
func (n *NinePNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	span := StartSpan("fuse.Readdir")
	defer span.End()

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
		switch e.Type {
		case p9.TypeDir:
			mode = uint32(syscall.S_IFDIR | 0755)
		case p9.TypeSymlink:
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
var _ fs.FileFsyncer = (*NinePFileHandle)(nil)
var _ fs.FileFlusher = (*NinePFileHandle)(nil)

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
	log.Printf("Write called: path=%s, fid=%d, offset=%d, len=%d", fh.path, fh.fid, offset, len(data))

	// Invalidate cache - file size/mtime will change
	fh.client.attrCache.Invalidate(fh.path)

	n, err := fh.client.WriteAt(fh.fid, data, uint64(offset))
	if err != nil {
		log.Printf("Write FAILED for %s (fid=%d) at offset %d, len=%d: %v", fh.path, fh.fid, offset, len(data), err)
		return 0, syscall.EIO
	}

	log.Printf("Write succeeded: path=%s, wrote %d bytes", fh.path, n)
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

// Fsync syncs file data to storage
func (fh *NinePFileHandle) Fsync(ctx context.Context, flags uint32) syscall.Errno {
	if err := fh.client.Fsync(fh.fid); err != nil {
		log.Printf("Fsync failed for %s: %v", fh.path, err)
		return syscall.EIO
	}
	return 0
}

// Flush is called when a file descriptor is closed
func (fh *NinePFileHandle) Flush(ctx context.Context) syscall.Errno {
	// Flush any cached writes
	if err := fh.client.Fsync(fh.fid); err != nil {
		log.Printf("Flush failed for %s: %v", fh.path, err)
		// Don't fail on flush errors
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

	// Disable caching to avoid stale data after operations like rename
	out.SetTimeout(0)
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

	// Disable caching to avoid stale data after operations like rename
	out.SetEntryTimeout(0)
	out.SetAttrTimeout(0)
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
