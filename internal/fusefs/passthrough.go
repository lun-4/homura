package fusefs

import (
	"github.com/hanwen/go-fuse/v2/fs"
)

// PassthroughRoot creates a loopback FUSE root that mirrors the source directory
// This uses go-fuse's built-in LoopbackRoot which handles all FUSE operations
// by forwarding them to the underlying filesystem
func PassthroughRoot(source string) (fs.InodeEmbedder, error) {
	return fs.NewLoopbackRoot(source)
}
