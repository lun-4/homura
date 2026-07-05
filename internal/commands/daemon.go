package commands

import "github.com/lun-4/homura/internal/daemon"

// DaemonRun runs the homura daemon in the foreground (it is spawned detached by
// EnsureDaemon). It blocks until the daemon idle-exits or is signalled.
func DaemonRun() error {
	return daemon.NewServer().Run()
}
