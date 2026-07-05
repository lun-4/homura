package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lun-4/homura/internal/vm"
)

const (
	// defaultIdleTimeout is how long the daemon waits with no VMs and no
	// in-flight RPCs before exiting. Respawn is cheap, so we don't linger.
	defaultIdleTimeout = 60 * time.Second
	// shutdownGrace is how long Shutdown() waits after SIGTERM before SIGKILL.
	shutdownGrace = 10 * time.Second
	// shutdownWait is how long stop-vm waits for the supervision goroutine to
	// finish cleanup after the VM process exits.
	shutdownWait = 15 * time.Second
	// maxLine bounds a single request/response line.
	maxLine = 4 * 1024 * 1024
)

// managedVM tracks a VM the daemon owns and its supervision state.
type managedVM struct {
	vm          *vm.VM
	slot        int
	consoleLog  string
	done        chan struct{} // closed once supervision + cleanup finishes
	cleanupOnce sync.Once
}

// Server is the central homura daemon: it owns every detached VM, streams
// build/boot progress over RPC, and idle-exits when nothing is running.
type Server struct {
	socketPath string
	lockFile   *os.File
	listener   net.Listener
	daemonLog  io.Writer

	idleTimeout time.Duration

	// startMu serializes start-vm so the global vm.ChildOutput / slog swap is
	// safe (only one build streams to a client at a time).
	startMu sync.Mutex

	// mu guards vms, inflight, and idleTimer.
	mu        sync.Mutex
	vms       map[int]*managedVM
	inflight  int
	idleTimer *time.Timer

	exitOnce sync.Once
}

// NewServer constructs a Server with default timings.
func NewServer() *Server {
	return &Server{
		vms:         make(map[int]*managedVM),
		idleTimeout: defaultIdleTimeout,
	}
}

// Run acquires the daemon lock, listens on the control socket, and serves RPCs
// until idle-exit or a termination signal. If another daemon already holds the
// lock, Run returns nil (this process loses the spawn race and exits cleanly).
func (s *Server) Run() error {
	lockPath, err := LockPath()
	if err != nil {
		return err
	}
	s.socketPath, err = SocketPath()
	if err != nil {
		return err
	}

	// Acquire the exclusive lock. A loser of a concurrent spawn race exits 0.
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open lock file %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			// Another daemon is already running - nothing to do.
			return nil
		}
		return fmt.Errorf("failed to lock %s: %w", lockPath, err)
	}
	s.lockFile = lockFile
	// Write our PID for diagnostics only (the flock, not this, is authoritative).
	lockFile.Truncate(0)
	lockFile.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)

	// Point our own logging at the daemon log file. The spawner also directs
	// our stdio here; from now on the daemon prints nothing user-facing.
	logPath, err := DaemonLogPath()
	if err != nil {
		s.releaseLock()
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		s.releaseLock()
		return fmt.Errorf("failed to open daemon log %s: %w", logPath, err)
	}
	s.daemonLog = logFile
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Daemon-spawned children (passt/virtiofsd/9passthrough/qemu) die with us.
	vm.ChildProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	vm.ChildOutput = logFile

	// Remove any stale socket, then listen.
	os.Remove(s.socketPath)
	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		s.releaseLock()
		return fmt.Errorf("failed to listen on %s: %w", s.socketPath, err)
	}
	os.Chmod(s.socketPath, 0o600)
	s.listener = listener

	slog.Info("homura daemon started", "pid", os.Getpid(), "socket", s.socketPath, "version", vm.VMImplementationVersion)

	// Handle termination signals: stop all VMs, then exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		slog.Info("received signal, stopping all VMs", "signal", sig)
		s.stopAllVMs()
		s.triggerExit()
	}()

	// Arm the idle timer immediately (we start with no VMs).
	s.mu.Lock()
	s.updateIdleLocked()
	s.mu.Unlock()

	// Accept loop. Returns when the listener is closed (idle-exit or signal).
	s.serve()

	// Final teardown.
	os.Remove(s.socketPath)
	s.releaseLock()
	slog.Info("homura daemon stopped")
	return nil
}

// releaseLock unlocks and closes the lock file if held.
func (s *Server) releaseLock() {
	if s.lockFile != nil {
		syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_UN)
		s.lockFile.Close()
		s.lockFile = nil
	}
}

// triggerExit closes the listener, unblocking the accept loop so Run returns.
func (s *Server) triggerExit() {
	s.exitOnce.Do(func() {
		if s.listener != nil {
			s.listener.Close()
		}
	})
}

func (s *Server) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Listener closed - time to exit.
			return
		}
		go s.handleConn(conn)
	}
}

// updateIdleLocked (re)arms or cancels the idle-exit timer based on current
// state. Must be called with s.mu held.
func (s *Server) updateIdleLocked() {
	idle := len(s.vms) == 0 && s.inflight == 0
	if idle {
		if s.idleTimer == nil {
			s.idleTimer = time.AfterFunc(s.idleTimeout, s.onIdleFire)
		}
	} else if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
}

func (s *Server) onIdleFire() {
	s.mu.Lock()
	idle := len(s.vms) == 0 && s.inflight == 0
	s.mu.Unlock()
	if idle {
		slog.Info("daemon idle, shutting down")
		s.triggerExit()
	}
}

// handleConn reads a single request, dispatches it, and closes the connection.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	s.mu.Lock()
	s.inflight++
	s.updateIdleLocked()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inflight--
		s.updateIdleLocked()
		s.mu.Unlock()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	if !scanner.Scan() {
		return
	}

	var req Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		cw := &connWriter{conn: conn}
		cw.sendError(0, CodeInvalidRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	cw := &connWriter{conn: conn}
	s.dispatch(cw, &req)
}

func (s *Server) dispatch(cw *connWriter, req *Request) {
	switch req.Method {
	case MethodPing:
		cw.sendResult(req.ID, PingResult{PID: os.Getpid(), Version: vm.VMImplementationVersion})
	case MethodStartVM:
		s.handleStartVM(cw, req)
	case MethodStopVM:
		s.handleStopVM(cw, req)
	case MethodListVMs:
		s.handleListVMs(cw, req)
	case MethodShutdown:
		s.handleShutdown(cw, req)
	default:
		cw.sendError(req.ID, CodeInvalidRequest, fmt.Sprintf("unknown method: %s", req.Method))
	}
}

func (s *Server) handleStartVM(cw *connWriter, req *Request) {
	var params StartVMParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		cw.sendError(req.ID, CodeInvalidRequest, fmt.Sprintf("invalid params: %v", err))
		return
	}

	// Serialize starts: only one build streams to a client at a time, because
	// the ChildOutput/slog swap below is process-global.
	s.startMu.Lock()
	defer s.startMu.Unlock()

	// Stream build/boot output to this client as log events (and to the daemon
	// log) by swapping the global writers for the duration of the start.
	ew := &eventWriter{cw: cw, id: req.ID}
	multi := io.MultiWriter(s.daemonLog, ew)
	prevChildOutput := vm.ChildOutput
	prevSlog := slog.Default()
	vm.ChildOutput = multi
	slog.SetDefault(slog.New(slog.NewTextHandler(multi, &slog.HandlerOptions{Level: slog.LevelInfo})))
	restore := func() {
		ew.Flush()
		vm.ChildOutput = prevChildOutput
		slog.SetDefault(prevSlog)
	}

	shareMode, err := vm.ParseShareMode(params.ShareMode)
	if err != nil {
		restore()
		cw.sendError(req.ID, CodeInvalidRequest, err.Error())
		return
	}

	var vmInst *vm.VM
	fail := func(code int, format string, a ...any) {
		if vmInst != nil {
			vmInst.Cleanup()
		}
		restore()
		cw.sendError(req.ID, code, fmt.Sprintf(format, a...))
	}

	vmInst, err = vm.NewVM(shareMode, params.WorkDir, params.StateDirBase)
	if err != nil {
		fail(CodeInternal, "failed to create VM: %v", err)
		return
	}

	consoleLog, err := ConsoleLogPath(vmInst.SlotNumber)
	if err != nil {
		fail(CodeInternal, "failed to resolve console log path: %v", err)
		return
	}
	consoleSock := filepath.Join(vmInst.StateDir, "console.sock")

	if err := vmInst.Start(vm.StartOptions{ConsoleSocket: consoleSock, ConsoleLog: consoleLog}); err != nil {
		fail(CodeInternal, "failed to start VM: %v", err)
		return
	}

	mvm := &managedVM{
		vm:         vmInst,
		slot:       vmInst.SlotNumber,
		consoleLog: consoleLog,
		done:       make(chan struct{}),
	}
	s.mu.Lock()
	s.vms[mvm.slot] = mvm
	s.updateIdleLocked()
	s.mu.Unlock()

	go s.supervise(mvm)

	restore()

	info := vmInst.ConnectionInfo()
	cw.sendResult(req.ID, VMResult{
		Slot:          info.SlotNumber,
		IP:            info.IPAddress,
		SSHPort:       info.SSHPort,
		PortStart:     info.PortStart,
		PortEnd:       info.PortEnd,
		ConsoleSocket: consoleSock,
		ConsoleLog:    consoleLog,
		QEMUPID:       vmInst.QEMUCmd.Process.Pid,
		ShareMode:     string(info.ShareMode),
	})
}

// supervise waits for a VM's QEMU process to exit, cleans it up, and
// deregisters it. It owns QEMUCmd.Wait() (the reaper), so Shutdown() only
// signals and never reaps.
func (s *Server) supervise(mvm *managedVM) {
	waitErr := mvm.vm.QEMUCmd.Wait()
	mvm.cleanupOnce.Do(func() {
		if cerr := mvm.vm.Cleanup(); cerr != nil {
			slog.Warn("Failed to clean up VM", "slot", mvm.slot, "error", cerr)
		}
	})
	slog.Info("VM exited", "slot", mvm.slot, "wait_err", waitErr,
		"console_log", mvm.consoleLog)
	slog.Info("console log preserved", "path", mvm.consoleLog)

	s.mu.Lock()
	delete(s.vms, mvm.slot)
	s.updateIdleLocked()
	s.mu.Unlock()

	close(mvm.done)
}

// stopManaged signals a VM to stop and waits for its supervision goroutine to
// finish cleanup.
func (s *Server) stopManaged(mvm *managedVM) error {
	if err := mvm.vm.Shutdown(shutdownGrace); err != nil {
		return fmt.Errorf("failed to signal VM slot %d: %w", mvm.slot, err)
	}
	select {
	case <-mvm.done:
		return nil
	case <-time.After(shutdownWait):
		return fmt.Errorf("VM slot %d did not exit within %s", mvm.slot, shutdownWait)
	}
}

func (s *Server) handleStopVM(cw *connWriter, req *Request) {
	var params StopVMParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		cw.sendError(req.ID, CodeInvalidRequest, fmt.Sprintf("invalid params: %v", err))
		return
	}

	s.mu.Lock()
	mvm, ok := s.vms[params.Slot]
	s.mu.Unlock()
	if !ok {
		cw.sendError(req.ID, CodeUnknownVM, fmt.Sprintf("no daemon-owned VM in slot %d", params.Slot))
		return
	}

	if err := s.stopManaged(mvm); err != nil {
		cw.sendError(req.ID, CodeInternal, err.Error())
		return
	}

	cw.sendResult(req.ID, StopVMResult{Slot: mvm.slot, ConsoleLog: mvm.consoleLog})
}

func (s *Server) handleListVMs(cw *connWriter, req *Request) {
	s.mu.Lock()
	results := make([]VMResult, 0, len(s.vms))
	for _, mvm := range s.vms {
		info := mvm.vm.ConnectionInfo()
		qemuPID := 0
		if mvm.vm.QEMUCmd != nil && mvm.vm.QEMUCmd.Process != nil {
			qemuPID = mvm.vm.QEMUCmd.Process.Pid
		}
		results = append(results, VMResult{
			Slot:          info.SlotNumber,
			IP:            info.IPAddress,
			SSHPort:       info.SSHPort,
			PortStart:     info.PortStart,
			PortEnd:       info.PortEnd,
			ConsoleSocket: filepath.Join(mvm.vm.StateDir, "console.sock"),
			ConsoleLog:    mvm.consoleLog,
			QEMUPID:       qemuPID,
			ShareMode:     string(info.ShareMode),
		})
	}
	s.mu.Unlock()

	cw.sendResult(req.ID, ListVMsResult{VMs: results})
}

func (s *Server) handleShutdown(cw *connWriter, req *Request) {
	stopped := s.stopAllVMs()
	cw.sendResult(req.ID, ShutdownResult{Stopped: stopped})
	// Exit after the response is written.
	s.triggerExit()
}

// stopAllVMs stops every managed VM and returns the count stopped.
func (s *Server) stopAllVMs() int {
	s.mu.Lock()
	list := make([]*managedVM, 0, len(s.vms))
	for _, mvm := range s.vms {
		list = append(list, mvm)
	}
	s.mu.Unlock()

	for _, mvm := range list {
		if err := s.stopManaged(mvm); err != nil {
			slog.Warn("Failed to stop VM during shutdown", "slot", mvm.slot, "error", err)
		}
	}
	return len(list)
}

// connWriter serializes frame writes on a single connection so that streamed
// log events and the final result/error never interleave.
type connWriter struct {
	mu   sync.Mutex
	conn net.Conn
}

func (cw *connWriter) writeFrame(resp Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	cw.mu.Lock()
	defer cw.mu.Unlock()
	_, err = cw.conn.Write(data)
	return err
}

func (cw *connWriter) sendResult(id int, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return cw.sendError(id, CodeInternal, fmt.Sprintf("failed to marshal result: %v", err))
	}
	return cw.writeFrame(Response{Result: raw, ID: id})
}

func (cw *connWriter) sendError(id, code int, msg string) error {
	return cw.writeFrame(Response{Error: &Error{Code: code, Message: msg}, ID: id})
}

// eventWriter is a line-buffered io.Writer that emits each complete line as an
// {"event":"log"} frame on the connection. Partial lines are held until the
// next newline or an explicit Flush.
type eventWriter struct {
	cw  *connWriter
	id  int
	mu  sync.Mutex
	buf []byte
}

func (w *eventWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

// Flush emits any buffered partial line.
func (w *eventWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
	}
}

func (w *eventWriter) emit(line string) {
	data, err := json.Marshal(LogEvent{Line: line})
	if err != nil {
		return
	}
	w.cw.writeFrame(Response{Event: "log", Data: data, ID: w.id})
}
