package fusefs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Server wraps a FUSE server with lifecycle management
type Server struct {
	server     *fuse.Server
	mountPoint string
}

// StartServer creates and starts a FUSE loopback server with write filtering
// mountPoint is where the FUSE filesystem will be mounted
// source is the directory to mirror (typically "/")
// allowedWritePaths are the paths where writes are permitted
func StartServer(ctx context.Context, mountPoint, source string, allowedWritePaths []string) (*Server, error) {
	slog.Info("starting FUSE server", "mountPoint", mountPoint, "source", source, "allowedWritePaths", allowedWritePaths)

	// Create filtered loopback root
	root := NewFilteredLoopbackRoot(source, allowedWritePaths)

	// Set up mount options
	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			// Filesystem name shown in mount output
			FsName: "homura-sandbox",
			Name:   "homura",
		},
		// Use entry/attr timeouts for better performance
		AttrTimeout:  &[]time.Duration{time.Second}[0],
		EntryTimeout: &[]time.Duration{time.Second}[0],
	}

	// Mount the filesystem
	// fs.Mount already starts serving in a background goroutine
	server, err := fs.Mount(mountPoint, root, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to mount FUSE filesystem: %w", err)
	}

	// Wait for the mount to be ready
	if err := server.WaitMount(); err != nil {
		server.Unmount()
		return nil, fmt.Errorf("failed to wait for FUSE mount: %w", err)
	}

	slog.Info("FUSE filesystem mounted successfully")

	// Handle context cancellation for cleanup
	go func() {
		<-ctx.Done()
		slog.Info("context cancelled, unmounting FUSE")
		server.Unmount()
	}()

	return &Server{
		server:     server,
		mountPoint: mountPoint,
	}, nil
}

// Unmount cleanly unmounts the FUSE filesystem
func (s *Server) Unmount() error {
	slog.Info("unmounting FUSE filesystem", "mountPoint", s.mountPoint)
	return s.server.Unmount()
}

// Wait blocks until the FUSE server exits
func (s *Server) Wait() {
	s.server.Wait()
}
