package main

import (
	"hash/fnv"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hugelgupf/p9/p9"
)

// VirtualRoot implements p9.Attacher for the virtual filesystem root
type VirtualRoot struct {
	Registry *PathRegistry
}

// Attach implements p9.Attacher
func (vr *VirtualRoot) Attach() (p9.File, error) {
	log.Printf("Attach() called - returning root VirtualFile")
	return &VirtualFile{
		path:     "/",
		registry: vr.Registry,
		realFile: nil,
	}, nil
}

// VirtualFile implements p9.File with path filtering
type VirtualFile struct {
	path           string
	registry       *PathRegistry
	realFile       *os.File // For opened files
	openedReadOnly bool     // Track if file was opened read-only
}

// Walk implements p9.File.Walk
func (vf *VirtualFile) Walk(names []string) ([]p9.QID, p9.File, error) {
	currentPath := vf.path
	qids := make([]p9.QID, 0, len(names))
	log.Printf("Walk(%s): names=%v", vf.path, names)

	for i, name := range names {
		// Handle parent directory
		if name == ".." {
			currentPath = path.Dir(currentPath)
		} else if name != "." {
			currentPath = path.Join(currentPath, name)
		}

		// Check visibility
		visibility := vf.registry.CheckPath(currentPath)
		log.Printf("Walk(%s): step %d, name=%s, currentPath=%s, visibility=%v", vf.path, i, name, currentPath, visibility)

		switch visibility {
		case NotVisible:
			log.Printf("Walk(%s): returning ENOENT for %s", vf.path, currentPath)
			return nil, nil, syscall.ENOENT

		case Exposed:
			// Stat the real file
			info, err := os.Lstat(currentPath)
			if err != nil {
				return nil, nil, err
			}

			qid := fileInfoToQID(currentPath, info)
			qids = append(qids, qid)

		case VirtualAncestor:
			// Generate synthetic QID for virtual directory
			qid := virtualDirQID(currentPath)
			qids = append(qids, qid)
		}
	}

	// Return new VirtualFile for the walked path
	return qids, &VirtualFile{
		path:     currentPath,
		registry: vf.registry,
		realFile: nil,
	}, nil
}

// WalkGetAttr implements p9.File.WalkGetAttr (optimization for Walk + GetAttr)
func (vf *VirtualFile) WalkGetAttr(names []string) ([]p9.QID, p9.File, p9.AttrMask, p9.Attr, error) {
	qids, file, err := vf.Walk(names)
	if err != nil {
		return nil, nil, p9.AttrMask{}, p9.Attr{}, err
	}

	// Get attributes of the final file
	_, mask, attr, err := file.GetAttr(p9.AttrMaskAll)
	if err != nil {
		return nil, nil, p9.AttrMask{}, p9.Attr{}, err
	}

	return qids, file, mask, attr, nil
}

// StatFS implements p9.File.StatFS
func (vf *VirtualFile) StatFS() (p9.FSStat, error) {
	visibility := vf.registry.CheckPath(vf.path)

	if visibility == Exposed {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(vf.path, &stat); err != nil {
			return p9.FSStat{}, err
		}

		return p9.FSStat{
			Type:            uint32(stat.Type),
			BlockSize:       uint32(stat.Bsize),
			Blocks:          stat.Blocks,
			BlocksFree:      stat.Bfree,
			BlocksAvailable: stat.Bavail,
			Files:           stat.Files,
			FilesFree:       stat.Ffree,
			NameLength:      uint32(stat.Namelen),
		}, nil
	}

	// Return synthetic statfs for virtual directories
	return p9.FSStat{
		Type:            0x01021997, // V9FS_MAGIC
		BlockSize:       4096,
		Blocks:          1000000,
		BlocksFree:      1000000,
		BlocksAvailable: 1000000,
		Files:           1000,
		FilesFree:       1000,
		NameLength:      255,
	}, nil
}

// GetAttr implements p9.File.GetAttr
func (vf *VirtualFile) GetAttr(req p9.AttrMask) (p9.QID, p9.AttrMask, p9.Attr, error) {
	visibility := vf.registry.CheckPath(vf.path)
	log.Printf("GetAttr(%s): visibility=%v", vf.path, visibility)

	switch visibility {
	case NotVisible:
		return p9.QID{}, p9.AttrMask{}, p9.Attr{}, syscall.ENOENT

	case Exposed:
		// Get real file attributes
		info, err := os.Lstat(vf.path)
		if err != nil {
			return p9.QID{}, p9.AttrMask{}, p9.Attr{}, err
		}

		qid := fileInfoToQID(vf.path, info)
		attr := fileInfoToAttr(vf.path, info)
		log.Printf("GetAttr(%s): returning exposed with mode=%o, uid=%d, gid=%d", vf.path, attr.Mode, attr.UID, attr.GID)
		return qid, p9.AttrMaskAll, attr, nil

	case VirtualAncestor:
		// Return synthetic directory attributes
		qid := virtualDirQID(vf.path)
		attr := p9.Attr{
			Mode:  p9.ModeDirectory | 0755,
			UID:   p9.UID(0),
			GID:   p9.GID(0),
			NLink: 2,
			Size:  4096,
		}
		log.Printf("GetAttr(%s): returning virtual dir with mode=%o", vf.path, attr.Mode)
		return qid, p9.AttrMaskAll, attr, nil
	}

	return p9.QID{}, p9.AttrMask{}, p9.Attr{}, syscall.ENOENT
}

// SetAttr implements p9.File.SetAttr
func (vf *VirtualFile) SetAttr(valid p9.SetAttrMask, attr p9.SetAttr) error {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return syscall.EPERM
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return syscall.EROFS
	}

	if valid.Size {
		if err := os.Truncate(vf.path, int64(attr.Size)); err != nil {
			return err
		}
	}

	if valid.Permissions {
		if err := os.Chmod(vf.path, os.FileMode(attr.Permissions)); err != nil {
			return err
		}
	}

	if valid.UID || valid.GID {
		uid := -1
		gid := -1
		if valid.UID {
			uid = int(attr.UID)
		}
		if valid.GID {
			gid = int(attr.GID)
		}
		if err := os.Lchown(vf.path, uid, gid); err != nil {
			// Chown failed - this is expected if server doesn't run as root
			log.Printf("SetAttr(%s): lchown failed (non-fatal): %v - ownership unchanged", vf.path, err)
		}
	}

	if valid.ATime || valid.MTime {
		// Get current times first - we'll use these for any times not being updated
		info, err := os.Lstat(vf.path)
		if err != nil {
			return err
		}

		atime := info.ModTime() // Default to current mtime (best we can do without syscall)
		mtime := info.ModTime()

		// Update atime if provided
		if valid.ATime {
			atime = time.Unix(int64(attr.ATimeSeconds), int64(attr.ATimeNanoSeconds))
		}

		// Update mtime if provided
		if valid.MTime {
			mtime = time.Unix(int64(attr.MTimeSeconds), int64(attr.MTimeNanoSeconds))
		}

		// Apply the time changes
		if err := os.Chtimes(vf.path, atime, mtime); err != nil {
			return err
		}
	}

	return nil
}

// Open implements p9.File.Open
func (vf *VirtualFile) Open(mode p9.OpenFlags) (p9.QID, uint32, error) {
	visibility := vf.registry.CheckPath(vf.path)
	log.Printf("Open(%s): visibility=%v, mode=%v", vf.path, visibility, mode)

	switch visibility {
	case NotVisible:
		return p9.QID{}, 0, syscall.ENOENT

	case Exposed:
		// Check if it's a directory first
		info, err := os.Lstat(vf.path)
		if err != nil {
			return p9.QID{}, 0, err
		}

		// Check if path is read-only and write mode is requested
		isReadOnly := vf.registry.IsReadOnly(vf.path)
		if isReadOnly && mode != p9.ReadOnly {
			return p9.QID{}, 0, syscall.EROFS
		}

		if info.IsDir() {
			// For directories, we don't need to actually open them
			// Just return the QID (Readdir will work without realFile set)
			qid := fileInfoToQID(vf.path, info)
			iounit := uint32(8192)
			log.Printf("Open(%s): opened exposed directory", vf.path)
			return qid, iounit, nil
		}

		// For regular files, open them
		flags := mode.OSFlags()
		f, err := os.OpenFile(vf.path, flags, 0)
		if err != nil {
			return p9.QID{}, 0, err
		}

		vf.realFile = f
		vf.openedReadOnly = isReadOnly

		qid := fileInfoToQID(vf.path, info)
		iounit := uint32(65536) // 64KB I/O unit

		log.Printf("Open(%s): opened exposed file", vf.path)
		return qid, iounit, nil

	case VirtualAncestor:
		// Virtual directories can be opened for reading (for readdir)
		if mode != p9.ReadOnly {
			return p9.QID{}, 0, syscall.EPERM
		}

		// Return synthetic QID for virtual directory
		qid := virtualDirQID(vf.path)
		iounit := uint32(8192)

		log.Printf("Open(%s): opened virtual directory", vf.path)
		return qid, iounit, nil
	}

	return p9.QID{}, 0, syscall.ENOENT
}

// Create implements p9.File.Create
func (vf *VirtualFile) Create(name string, flags p9.OpenFlags, permissions p9.FileMode, uid p9.UID, gid p9.GID) (p9.File, p9.QID, uint32, error) {
	visibility := vf.registry.CheckPath(vf.path)
	if visibility != Exposed {
		return nil, p9.QID{}, 0, syscall.EPERM
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return nil, p9.QID{}, 0, syscall.EROFS
	}

	newPath := path.Join(vf.path, name)

	// Convert p9 open flags to OS flags
	osFlags := flags.OSFlags()
	osFlags |= os.O_CREATE | os.O_EXCL

	// Create the file
	f, err := os.OpenFile(newPath, osFlags, os.FileMode(permissions))
	if err != nil {
		return nil, p9.QID{}, 0, err
	}

	// Set ownership if specified
	// Note: chown may fail if server doesn't run as root - this is non-fatal
	if uid != p9.NoUID || gid != p9.NoGID {
		uidInt := -1
		gidInt := -1
		if uid != p9.NoUID {
			uidInt = int(uid)
		}
		if gid != p9.NoGID {
			gidInt = int(gid)
		}
		if err := f.Chown(uidInt, gidInt); err != nil {
			// Chown failed - this is expected if server doesn't run as root
			// Just log a warning and continue with default ownership
			log.Printf("Create(%s/%s): chown failed (non-fatal), using default ownership", vf.path, name)
		}
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		os.Remove(newPath)
		return nil, p9.QID{}, 0, err
	}

	qid := fileInfoToQID(newPath, info)
	iounit := uint32(65536)

	newFile := &VirtualFile{
		path:     newPath,
		registry: vf.registry,
		realFile: f,
	}

	log.Printf("Create(%s/%s): created successfully", vf.path, name)
	return newFile, qid, iounit, nil
}

// Mkdir implements p9.File.Mkdir
func (vf *VirtualFile) Mkdir(name string, permissions p9.FileMode, uid p9.UID, gid p9.GID) (p9.QID, error) {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return p9.QID{}, syscall.EPERM
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return p9.QID{}, syscall.EROFS
	}

	newPath := path.Join(vf.path, name)

	if err := os.Mkdir(newPath, os.FileMode(permissions)); err != nil {
		return p9.QID{}, err
	}

	// Set ownership if specified
	// Note: chown may fail if server doesn't run as root - this is non-fatal
	if uid != p9.NoUID || gid != p9.NoGID {
		uidInt := -1
		gidInt := -1
		if uid != p9.NoUID {
			uidInt = int(uid)
		}
		if gid != p9.NoGID {
			gidInt = int(gid)
		}
		if err := os.Chown(newPath, uidInt, gidInt); err != nil {
			// Chown failed - this is expected if server doesn't run as root
			log.Printf("Mkdir(%s): chown failed (non-fatal): %v - directory will use default ownership", vf.path, err)
		}
	}

	info, err := os.Lstat(newPath)
	if err != nil {
		return p9.QID{}, err
	}

	return fileInfoToQID(newPath, info), nil
}

// Symlink implements p9.File.Symlink
func (vf *VirtualFile) Symlink(oldname string, newname string, uid p9.UID, gid p9.GID) (p9.QID, error) {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return p9.QID{}, syscall.EPERM
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return p9.QID{}, syscall.EROFS
	}

	newPath := path.Join(vf.path, newname)

	if err := os.Symlink(oldname, newPath); err != nil {
		return p9.QID{}, err
	}

	// Set ownership if specified (note: lchown for symlinks)
	// Note: chown may fail if server doesn't run as root - this is non-fatal
	if uid != p9.NoUID || gid != p9.NoGID {
		uidInt := -1
		gidInt := -1
		if uid != p9.NoUID {
			uidInt = int(uid)
		}
		if gid != p9.NoGID {
			gidInt = int(gid)
		}
		if err := os.Lchown(newPath, uidInt, gidInt); err != nil {
			// Chown failed - this is expected if server doesn't run as root
			log.Printf("Symlink(%s): lchown failed (non-fatal): %v - symlink will use default ownership", vf.path, err)
		}
	}

	info, err := os.Lstat(newPath)
	if err != nil {
		return p9.QID{}, err
	}

	return fileInfoToQID(newPath, info), nil
}

// Link implements p9.File.Link
func (vf *VirtualFile) Link(target p9.File, newname string) error {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return syscall.EPERM
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return syscall.EROFS
	}

	targetVF, ok := target.(*VirtualFile)
	if !ok {
		return syscall.EINVAL
	}

	if vf.registry.CheckPath(targetVF.path) != Exposed {
		return syscall.EPERM
	}

	newPath := path.Join(vf.path, newname)
	return os.Link(targetVF.path, newPath)
}

// Mknod implements p9.File.Mknod
func (vf *VirtualFile) Mknod(name string, mode p9.FileMode, major uint32, minor uint32, uid p9.UID, gid p9.GID) (p9.QID, error) {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return p9.QID{}, syscall.EPERM
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return p9.QID{}, syscall.EROFS
	}

	newPath := path.Join(vf.path, name)
	dev := uint64(major)<<32 | uint64(minor)

	if err := syscall.Mknod(newPath, uint32(mode), int(dev)); err != nil {
		return p9.QID{}, err
	}

	// Set ownership if specified
	// Note: chown may fail if server doesn't run as root - this is non-fatal
	if uid != p9.NoUID || gid != p9.NoGID {
		uidInt := -1
		gidInt := -1
		if uid != p9.NoUID {
			uidInt = int(uid)
		}
		if gid != p9.NoGID {
			gidInt = int(gid)
		}
		if err := os.Lchown(newPath, uidInt, gidInt); err != nil {
			// Chown failed - this is expected if server doesn't run as root
			log.Printf("Mknod(%s): lchown failed (non-fatal): %v - node will use default ownership", vf.path, err)
		}
	}

	info, err := os.Lstat(newPath)
	if err != nil {
		return p9.QID{}, err
	}

	return fileInfoToQID(newPath, info), nil
}

// Rename implements p9.File.Rename
func (vf *VirtualFile) Rename(directory p9.File, newname string) error {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return syscall.EPERM
	}

	// Check if source path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return syscall.EROFS
	}

	dirVF, ok := directory.(*VirtualFile)
	if !ok {
		return syscall.EINVAL
	}

	if vf.registry.CheckPath(dirVF.path) != Exposed {
		return syscall.EPERM
	}

	// Check if destination path is read-only
	if vf.registry.IsReadOnly(dirVF.path) {
		return syscall.EROFS
	}

	newPath := path.Join(dirVF.path, newname)
	return os.Rename(vf.path, newPath)
}

// RenameAt implements p9.File.RenameAt
func (vf *VirtualFile) RenameAt(oldname string, newdir p9.File, newname string) error {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return syscall.EPERM
	}

	// Check if source path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return syscall.EROFS
	}

	newdirVF, ok := newdir.(*VirtualFile)
	if !ok {
		return syscall.EINVAL
	}

	if vf.registry.CheckPath(newdirVF.path) != Exposed {
		return syscall.EPERM
	}

	// Check if destination path is read-only
	if vf.registry.IsReadOnly(newdirVF.path) {
		return syscall.EROFS
	}

	oldPath := path.Join(vf.path, oldname)
	newPath := path.Join(newdirVF.path, newname)

	return os.Rename(oldPath, newPath)
}

// UnlinkAt implements p9.File.UnlinkAt
func (vf *VirtualFile) UnlinkAt(name string, flags uint32) error {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return syscall.EPERM
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return syscall.EROFS
	}

	targetPath := path.Join(vf.path, name)
	return os.Remove(targetPath)
}

// Readdir implements p9.File.Readdir
func (vf *VirtualFile) Readdir(offset uint64, count uint32) (p9.Dirents, error) {
	visibility := vf.registry.CheckPath(vf.path)
	log.Printf("Readdir(%s): visibility=%v, offset=%d, count=%d", vf.path, visibility, offset, count)

	switch visibility {
	case NotVisible:
		return nil, syscall.ENOENT

	case Exposed:
		// Read real directory
		f, err := os.Open(vf.path)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		// Read all entries
		entries, err := f.Readdir(-1)
		if err != nil {
			return nil, err
		}

		// Convert to p9.Dirent
		dirents := make([]p9.Dirent, 0, len(entries))
		for i, entry := range entries {
			if uint64(i) < offset {
				continue
			}
			if count > 0 && uint32(len(dirents)) >= count {
				break
			}

			qid := fileInfoToQID(filepath.Join(vf.path, entry.Name()), entry)
			dirents = append(dirents, p9.Dirent{
				QID:    qid,
				Type:   qidType(entry),
				Name:   entry.Name(),
				Offset: uint64(i + 1),
			})
		}

		log.Printf("Readdir(%s): returning %d entries (exposed)", vf.path, len(dirents))
		return dirents, nil

	case VirtualAncestor:
		// List only exposed children
		children := vf.registry.GetExposedChildren(vf.path)
		log.Printf("Readdir(%s): VirtualAncestor with %d children", vf.path, len(children))

		dirents := make([]p9.Dirent, 0, len(children))
		for i, name := range children {
			if uint64(i) < offset {
				continue
			}
			if count > 0 && uint32(len(dirents)) >= count {
				break
			}

			childPath := path.Join(vf.path, name)
			childVisibility := vf.registry.CheckPath(childPath)

			var qid p9.QID
			var dtype p9.QIDType

			if childVisibility == Exposed {
				// Stat the real file
				info, err := os.Lstat(childPath)
				if err != nil {
					log.Printf("Readdir(%s): failed to stat child %s: %v", vf.path, childPath, err)
					continue
				}
				qid = fileInfoToQID(childPath, info)
				dtype = qidType(info)
			} else {
				// Virtual directory
				qid = virtualDirQID(childPath)
				dtype = p9.TypeDir
			}

			dirents = append(dirents, p9.Dirent{
				QID:    qid,
				Type:   dtype,
				Name:   name,
				Offset: uint64(i + 1),
			})
		}

		log.Printf("Readdir(%s): returning %d entries (virtual)", vf.path, len(dirents))
		return dirents, nil
	}

	log.Printf("Readdir(%s): returning ENOENT", vf.path)
	return nil, syscall.ENOENT
}

// Readlink implements p9.File.Readlink
func (vf *VirtualFile) Readlink() (string, error) {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return "", syscall.EPERM
	}

	return os.Readlink(vf.path)
}

// ReadAt implements p9.File.ReadAt
func (vf *VirtualFile) ReadAt(p []byte, offset int64) (int, error) {
	if vf.realFile == nil {
		return 0, syscall.EBADF
	}

	return vf.realFile.ReadAt(p, offset)
}

// WriteAt implements p9.File.WriteAt
func (vf *VirtualFile) WriteAt(p []byte, offset int64) (int, error) {
	if vf.realFile == nil {
		return 0, syscall.EBADF
	}

	// Check if file was opened read-only
	if vf.openedReadOnly {
		return 0, syscall.EROFS
	}

	return vf.realFile.WriteAt(p, offset)
}

// FSync implements p9.File.FSync
func (vf *VirtualFile) FSync() error {
	if vf.realFile == nil {
		return nil
	}

	return vf.realFile.Sync()
}

// Close implements p9.File.Close
func (vf *VirtualFile) Close() error {
	if vf.realFile != nil {
		err := vf.realFile.Close()
		vf.realFile = nil
		return err
	}
	return nil
}

// GetXattr implements p9.File.GetXattr
func (vf *VirtualFile) GetXattr(name string) ([]byte, error) {
	log.Printf("GetXattr(%s): name=%s", vf.path, name)
	if vf.registry.CheckPath(vf.path) != Exposed {
		log.Printf("GetXattr(%s): path not exposed, returning EOPNOTSUPP", vf.path)
		return nil, syscall.EOPNOTSUPP
	}

	// Use syscall to get extended attribute
	size, err := syscall.Getxattr(vf.path, name, nil)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, size)
	_, err = syscall.Getxattr(vf.path, name, buf)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

// SetXattr implements p9.File.SetXattr
func (vf *VirtualFile) SetXattr(name string, value []byte, flags p9.XattrFlags) error {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return syscall.EOPNOTSUPP
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return syscall.EROFS
	}

	return syscall.Setxattr(vf.path, name, value, int(flags))
}

// ListXattrs implements p9.File.ListXattrs
func (vf *VirtualFile) ListXattrs() ([]string, error) {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return nil, syscall.EOPNOTSUPP
	}

	// Get size first
	size, err := syscall.Listxattr(vf.path, nil)
	if err != nil {
		return nil, err
	}

	if size == 0 {
		return []string{}, nil
	}

	buf := make([]byte, size)
	_, err = syscall.Listxattr(vf.path, buf)
	if err != nil {
		return nil, err
	}

	// Parse null-terminated list
	var attrs []string
	start := 0
	for i, b := range buf {
		if b == 0 {
			if i > start {
				attrs = append(attrs, string(buf[start:i]))
			}
			start = i + 1
		}
	}

	return attrs, nil
}

// RemoveXattr implements p9.File.RemoveXattr
func (vf *VirtualFile) RemoveXattr(name string) error {
	if vf.registry.CheckPath(vf.path) != Exposed {
		return syscall.EOPNOTSUPP
	}

	// Check if path is read-only
	if vf.registry.IsReadOnly(vf.path) {
		return syscall.EROFS
	}

	return syscall.Removexattr(vf.path, name)
}

// Lock implements p9.File.Lock
func (vf *VirtualFile) Lock(pid int, locktype p9.LockType, flags p9.LockFlags, start, length uint64, client string) (p9.LockStatus, error) {
	// Basic implementation - just return success
	// A full implementation would use fcntl for actual file locking
	return p9.LockStatusOK, nil
}

// Removed implements p9.File.Removed (called when file is removed)
func (vf *VirtualFile) Removed() {
	// Cleanup if needed
}

// Renamed implements p9.File.Renamed (called when file is renamed)
func (vf *VirtualFile) Renamed(newDir p9.File, newName string) {
	if dirVF, ok := newDir.(*VirtualFile); ok {
		vf.path = path.Join(dirVF.path, newName)
	}
}

// Helper functions

func fileInfoToQID(path string, info os.FileInfo) p9.QID {
	qid := p9.QID{
		Type: qidType(info),
	}

	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		qid.Path = stat.Ino
		qid.Version = uint32(stat.Mtim.Sec)
	} else {
		// Fallback: use hash of path
		h := fnv.New64a()
		h.Write([]byte(path))
		qid.Path = h.Sum64()
	}

	return qid
}

func virtualDirQID(path string) p9.QID {
	h := fnv.New64a()
	h.Write([]byte(path))

	return p9.QID{
		Type:    p9.TypeDir,
		Path:    h.Sum64(),
		Version: 0,
	}
}

func qidType(info os.FileInfo) p9.QIDType {
	var qtype p9.QIDType

	mode := info.Mode()
	if mode&os.ModeDir != 0 {
		qtype |= p9.TypeDir
	}
	if mode&os.ModeSymlink != 0 {
		qtype |= p9.TypeSymlink
	}

	return qtype
}

func fileInfoToAttr(path string, info os.FileInfo) p9.Attr {
	attr := p9.Attr{
		Mode:  p9.ModeFromOS(info.Mode()),
		Size:  uint64(info.Size()),
		NLink: 1,
	}

	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		attr.UID = p9.UID(stat.Uid)
		attr.GID = p9.GID(stat.Gid)
		attr.NLink = p9.NLink(stat.Nlink)
		attr.RDev = p9.Dev(stat.Rdev)
		attr.ATimeSeconds = uint64(stat.Atim.Sec)
		attr.ATimeNanoSeconds = uint64(stat.Atim.Nsec)
		attr.MTimeSeconds = uint64(stat.Mtim.Sec)
		attr.MTimeNanoSeconds = uint64(stat.Mtim.Nsec)
		attr.CTimeSeconds = uint64(stat.Ctim.Sec)
		attr.CTimeNanoSeconds = uint64(stat.Ctim.Nsec)
		attr.BlockSize = uint64(stat.Blksize)
		attr.Blocks = uint64(stat.Blocks)
	}

	return attr
}

// Ensure VirtualFile implements p9.File interface
var _ p9.File = (*VirtualFile)(nil)

// Ensure VirtualRoot implements p9.Attacher interface
var _ p9.Attacher = (*VirtualRoot)(nil)

// Add io.ReaderAt and io.WriterAt interfaces for compatibility
var _ io.ReaderAt = (*VirtualFile)(nil)
var _ io.WriterAt = (*VirtualFile)(nil)
