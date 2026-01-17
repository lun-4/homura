package fusefs

import (
	"context"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// pathToIno generates a unique inode number from a path
// Using path-based inodes avoids caching issues with host inode numbers
func pathToIno(path string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(path))
	return h.Sum64()
}

// FilterConfig holds the write filter configuration
type FilterConfig struct {
	AllowedWritePaths []string
}

// isWriteAllowed checks if writing to the given path is permitted
func (fc *FilterConfig) isWriteAllowed(path string) bool {
	cleanPath := filepath.Clean(path)

	for _, allowed := range fc.AllowedWritePaths {
		if cleanPath == allowed || strings.HasPrefix(cleanPath, allowed+"/") {
			return true
		}
	}
	return false
}

// FilteredLoopbackRoot is the root of a filtered loopback filesystem
type FilteredLoopbackRoot struct {
	fs.Inode
	rootPath string
	filter   *FilterConfig
}

// FilteredLoopbackNode is a node in the filtered loopback filesystem
type FilteredLoopbackNode struct {
	fs.Inode
	rootPath  string   // Host root path (e.g., "/")
	nodePath  string   // Path of this node relative to root (e.g., "home/luna")
	filter    *FilterConfig
}

// NewFilteredLoopbackRoot creates a new filtered loopback root
func NewFilteredLoopbackRoot(rootPath string, allowedWritePaths []string) *FilteredLoopbackRoot {
	normalized := make([]string, len(allowedWritePaths))
	for i, p := range allowedWritePaths {
		normalized[i] = filepath.Clean(p)
	}

	return &FilteredLoopbackRoot{
		rootPath: rootPath,
		filter: &FilterConfig{
			AllowedWritePaths: normalized,
		},
	}
}

// hostPath returns the actual filesystem path for this node
func (n *FilteredLoopbackRoot) hostPath() string {
	return n.rootPath
}

func (n *FilteredLoopbackNode) hostPath() string {
	return filepath.Join(n.rootPath, n.nodePath)
}

// fusePath returns the FUSE-visible path (what the user sees)
func (n *FilteredLoopbackRoot) fusePath() string {
	return "/"
}

func (n *FilteredLoopbackNode) fusePath() string {
	return "/" + n.nodePath
}

// newNode creates a child node with the given relative path
func (n *FilteredLoopbackRoot) newNode(name string) *FilteredLoopbackNode {
	return &FilteredLoopbackNode{
		rootPath: n.rootPath,
		nodePath: name,
		filter:   n.filter,
	}
}

func (n *FilteredLoopbackNode) newNode(name string) *FilteredLoopbackNode {
	return &FilteredLoopbackNode{
		rootPath: n.rootPath,
		nodePath: filepath.Join(n.nodePath, name),
		filter:   n.filter,
	}
}

// Statfs implements fs.NodeStatfser
func (n *FilteredLoopbackRoot) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	var st syscall.Statfs_t
	if err := syscall.Statfs(n.hostPath(), &st); err != nil {
		return fs.ToErrno(err)
	}
	out.FromStatfsT(&st)
	return 0
}

// Getattr implements fs.NodeGetattrer
func (n *FilteredLoopbackRoot) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	var st syscall.Stat_t
	if err := syscall.Lstat(n.hostPath(), &st); err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&st)
	return 0
}

func (n *FilteredLoopbackNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	var st syscall.Stat_t
	if err := syscall.Lstat(n.hostPath(), &st); err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&st)
	return 0
}

// Lookup implements fs.NodeLookuper
func (n *FilteredLoopbackRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := filepath.Join(n.hostPath(), name)
	var st syscall.Stat_t
	if err := syscall.Lstat(childPath, &st); err != nil {
		return nil, fs.ToErrno(err)
	}
	out.FromStat(&st)

	child := n.newNode(name)
	ino := pathToIno(child.fusePath())
	return n.NewInode(ctx, child, fs.StableAttr{Mode: st.Mode, Ino: ino}), 0
}

func (n *FilteredLoopbackNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := filepath.Join(n.hostPath(), name)
	var st syscall.Stat_t
	if err := syscall.Lstat(childPath, &st); err != nil {
		return nil, fs.ToErrno(err)
	}
	out.FromStat(&st)

	child := n.newNode(name)
	ino := pathToIno(child.fusePath())
	return n.NewInode(ctx, child, fs.StableAttr{Mode: st.Mode, Ino: ino}), 0
}

// Readdir implements fs.NodeReaddirer
func (n *FilteredLoopbackRoot) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return readDir(n.hostPath())
}

func (n *FilteredLoopbackNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return readDir(n.hostPath())
}

func readDir(path string) (fs.DirStream, syscall.Errno) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fs.ToErrno(err)
	}

	result := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, fuse.DirEntry{
			Name: e.Name(),
			Mode: uint32(info.Mode()),
		})
	}
	return fs.NewListDirStream(result), 0
}

// Readlink implements fs.NodeReadlinker
func (n *FilteredLoopbackNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	target, err := os.Readlink(n.hostPath())
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	return []byte(target), 0
}

// Open implements fs.NodeOpener
func (n *FilteredLoopbackNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	isWrite := flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_APPEND|syscall.O_CREAT|syscall.O_TRUNC) != 0
	if isWrite && !n.filter.isWriteAllowed(n.fusePath()) {
		return nil, 0, syscall.EROFS
	}

	fd, err := syscall.Open(n.hostPath(), int(flags), 0)
	if err != nil {
		return nil, 0, fs.ToErrno(err)
	}
	return &loopbackFile{fd: fd}, 0, 0
}

// Create implements fs.NodeCreater
func (n *FilteredLoopbackNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	child := n.newNode(name)
	if !n.filter.isWriteAllowed(child.fusePath()) {
		return nil, nil, 0, syscall.EROFS
	}

	childHostPath := child.hostPath()
	fd, err := syscall.Open(childHostPath, int(flags)|syscall.O_CREAT, mode)
	if err != nil {
		return nil, nil, 0, fs.ToErrno(err)
	}

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, nil, 0, fs.ToErrno(err)
	}
	out.FromStat(&st)

	ino := pathToIno(child.fusePath())
	inode := n.NewInode(ctx, child, fs.StableAttr{Mode: st.Mode, Ino: ino})
	return inode, &loopbackFile{fd: fd}, 0, 0
}

func (n *FilteredLoopbackRoot) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	child := n.newNode(name)
	if !n.filter.isWriteAllowed(child.fusePath()) {
		return nil, nil, 0, syscall.EROFS
	}

	childHostPath := child.hostPath()
	fd, err := syscall.Open(childHostPath, int(flags)|syscall.O_CREAT, mode)
	if err != nil {
		return nil, nil, 0, fs.ToErrno(err)
	}

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, nil, 0, fs.ToErrno(err)
	}
	out.FromStat(&st)

	ino := pathToIno(child.fusePath())
	inode := n.NewInode(ctx, child, fs.StableAttr{Mode: st.Mode, Ino: ino})
	return inode, &loopbackFile{fd: fd}, 0, 0
}

// Mkdir implements fs.NodeMkdirer
func (n *FilteredLoopbackNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	child := n.newNode(name)
	if !n.filter.isWriteAllowed(child.fusePath()) {
		return nil, syscall.EROFS
	}

	childHostPath := child.hostPath()
	if err := syscall.Mkdir(childHostPath, mode); err != nil {
		return nil, fs.ToErrno(err)
	}

	var st syscall.Stat_t
	if err := syscall.Lstat(childHostPath, &st); err != nil {
		return nil, fs.ToErrno(err)
	}
	out.FromStat(&st)

	ino := pathToIno(child.fusePath())
	return n.NewInode(ctx, child, fs.StableAttr{Mode: st.Mode, Ino: ino}), 0
}

// Unlink implements fs.NodeUnlinker
func (n *FilteredLoopbackNode) Unlink(ctx context.Context, name string) syscall.Errno {
	child := n.newNode(name)
	if !n.filter.isWriteAllowed(child.fusePath()) {
		return syscall.EROFS
	}
	return fs.ToErrno(syscall.Unlink(child.hostPath()))
}

// Rmdir implements fs.NodeRmdirer
func (n *FilteredLoopbackNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	child := n.newNode(name)
	if !n.filter.isWriteAllowed(child.fusePath()) {
		return syscall.EROFS
	}
	return fs.ToErrno(syscall.Rmdir(child.hostPath()))
}

// Rename implements fs.NodeRenamer
func (n *FilteredLoopbackNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	oldChild := n.newNode(name)

	var newChildPath string
	switch np := newParent.(type) {
	case *FilteredLoopbackNode:
		newChild := np.newNode(newName)
		newChildPath = newChild.fusePath()
	case *FilteredLoopbackRoot:
		newChildPath = "/" + newName
	default:
		return syscall.EINVAL
	}

	if !n.filter.isWriteAllowed(oldChild.fusePath()) || !n.filter.isWriteAllowed(newChildPath) {
		return syscall.EROFS
	}

	var newHostPath string
	switch np := newParent.(type) {
	case *FilteredLoopbackNode:
		newHostPath = filepath.Join(np.hostPath(), newName)
	case *FilteredLoopbackRoot:
		newHostPath = filepath.Join(np.hostPath(), newName)
	}

	return fs.ToErrno(syscall.Rename(oldChild.hostPath(), newHostPath))
}

// Symlink implements fs.NodeSymlinker
func (n *FilteredLoopbackNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	child := n.newNode(name)
	if !n.filter.isWriteAllowed(child.fusePath()) {
		return nil, syscall.EROFS
	}

	childHostPath := child.hostPath()
	if err := syscall.Symlink(target, childHostPath); err != nil {
		return nil, fs.ToErrno(err)
	}

	var st syscall.Stat_t
	if err := syscall.Lstat(childHostPath, &st); err != nil {
		return nil, fs.ToErrno(err)
	}
	out.FromStat(&st)

	ino := pathToIno(child.fusePath())
	return n.NewInode(ctx, child, fs.StableAttr{Mode: st.Mode, Ino: ino}), 0
}

// Setattr implements fs.NodeSetattrer
func (n *FilteredLoopbackNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if !n.filter.isWriteAllowed(n.fusePath()) {
		return syscall.EROFS
	}

	p := n.hostPath()

	if m, ok := in.GetMode(); ok {
		if err := syscall.Chmod(p, m); err != nil {
			return fs.ToErrno(err)
		}
	}

	if sz, ok := in.GetSize(); ok {
		if err := syscall.Truncate(p, int64(sz)); err != nil {
			return fs.ToErrno(err)
		}
	}

	var st syscall.Stat_t
	if err := syscall.Lstat(p, &st); err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&st)
	return 0
}

// loopbackFile is a simple file handle
type loopbackFile struct {
	fd int
}

var _ fs.FileReader = (*loopbackFile)(nil)
var _ fs.FileWriter = (*loopbackFile)(nil)
var _ fs.FileFlusher = (*loopbackFile)(nil)
var _ fs.FileReleaser = (*loopbackFile)(nil)
var _ fs.FileGetattrer = (*loopbackFile)(nil)

func (f *loopbackFile) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	n, err := syscall.Pread(f.fd, dest, off)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	return fuse.ReadResultData(dest[:n]), 0
}

func (f *loopbackFile) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	n, err := syscall.Pwrite(f.fd, data, off)
	if err != nil {
		return 0, fs.ToErrno(err)
	}
	return uint32(n), 0
}

func (f *loopbackFile) Flush(ctx context.Context) syscall.Errno {
	return 0
}

func (f *loopbackFile) Release(ctx context.Context) syscall.Errno {
	return fs.ToErrno(syscall.Close(f.fd))
}

func (f *loopbackFile) Getattr(ctx context.Context, out *fuse.AttrOut) syscall.Errno {
	var st syscall.Stat_t
	if err := syscall.Fstat(f.fd, &st); err != nil {
		return fs.ToErrno(err)
	}
	out.FromStat(&st)
	return 0
}

// Interface assertions
var _ fs.InodeEmbedder = (*FilteredLoopbackRoot)(nil)
var _ fs.InodeEmbedder = (*FilteredLoopbackNode)(nil)
var _ fs.NodeLookuper = (*FilteredLoopbackRoot)(nil)
var _ fs.NodeLookuper = (*FilteredLoopbackNode)(nil)
var _ fs.NodeReaddirer = (*FilteredLoopbackRoot)(nil)
var _ fs.NodeReaddirer = (*FilteredLoopbackNode)(nil)
var _ fs.NodeGetattrer = (*FilteredLoopbackRoot)(nil)
var _ fs.NodeGetattrer = (*FilteredLoopbackNode)(nil)
var _ fs.NodeStatfser = (*FilteredLoopbackRoot)(nil)
var _ fs.NodeCreater = (*FilteredLoopbackRoot)(nil)
var _ fs.NodeCreater = (*FilteredLoopbackNode)(nil)
var _ fs.NodeMkdirer = (*FilteredLoopbackNode)(nil)
var _ fs.NodeUnlinker = (*FilteredLoopbackNode)(nil)
var _ fs.NodeRmdirer = (*FilteredLoopbackNode)(nil)
var _ fs.NodeOpener = (*FilteredLoopbackNode)(nil)
var _ fs.NodeRenamer = (*FilteredLoopbackNode)(nil)
var _ fs.NodeSymlinker = (*FilteredLoopbackNode)(nil)
var _ fs.NodeSetattrer = (*FilteredLoopbackNode)(nil)
var _ fs.NodeReadlinker = (*FilteredLoopbackNode)(nil)
