package daemon

import (
	"testing"
	"time"

	"github.com/lun-4/homura/internal/vm"
)

// TestServerLifecycle exercises the daemon end-to-end without booting a VM:
// ping, list-vms (empty), the spawn-race lock loser, and idle-exit timing.
func TestServerLifecycle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	t.Setenv("HOME", tmp) // LogDir() derives from the home dir

	s := NewServer()
	s.idleTimeout = 400 * time.Millisecond

	errCh := make(chan error, 1)
	go func() { errCh <- s.Run() }()

	// Wait for the daemon to start listening.
	var c *Client
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cc, err := Dial(); err == nil {
			c = cc
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c == nil {
		t.Fatal("daemon did not come up")
	}

	// ping
	pid, version, err := c.Ping()
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("bad pid: %d", pid)
	}
	if version != vm.VMImplementationVersion {
		t.Fatalf("version = %d, want %d", version, vm.VMImplementationVersion)
	}

	// list-vms should be empty
	var lr ListVMsResult
	if err := c.Call(MethodListVMs, nil, &lr); err != nil {
		t.Fatalf("list-vms: %v", err)
	}
	if len(lr.VMs) != 0 {
		t.Fatalf("expected 0 VMs, got %d", len(lr.VMs))
	}

	// A second daemon must lose the flock race and return nil immediately.
	s2 := NewServer()
	if err := s2.Run(); err != nil {
		t.Fatalf("second daemon Run() = %v, want nil (lock loser)", err)
	}

	// With no VMs and no in-flight RPCs, the daemon idle-exits.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not idle-exit")
	}
}
