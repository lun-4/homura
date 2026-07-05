package vm

import (
	"io"
	"os"
	"syscall"
)

// ChildOutput is where spawned child process output (docker builds, passt,
// virtiofsd, qemu in daemon mode) is written. The daemon points this at a
// log + RPC stream; the CLI default is stderr.
var ChildOutput io.Writer = os.Stderr

// ChildProcAttr, when non-nil, is applied to child processes (passt,
// virtiofsd, 9passthrough) so they die with the daemon (Pdeathsig). Left nil
// by the CLI so foreground behavior is unchanged; a future daemon sets it
// before spawning VMs.
var ChildProcAttr *syscall.SysProcAttr
