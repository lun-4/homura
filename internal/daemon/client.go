package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/lun-4/homura/internal/vm"
)

// Client is a connection factory for the homura daemon. Each RPC opens its own
// short-lived unix-socket connection (one connection per request).
type Client struct {
	socketPath string
}

// Dial connects to an already-running daemon. It returns an error if no daemon
// is listening (it does not spawn one - use EnsureDaemon for that).
func Dial() (*Client, error) {
	sock, err := SocketPath()
	if err != nil {
		return nil, err
	}
	// Probe with a quick connect so callers get a clear "not running" signal.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("daemon not running: %w", err)
	}
	conn.Close()
	return &Client{socketPath: sock}, nil
}

// EnsureDaemon returns a client to a running daemon, spawning one if needed.
// The spawn is race-safe: the daemon flocks its lock file, so if several
// clients spawn concurrently exactly one wins and the losers exit 0. After
// spawning we poll Dial+Ping until the socket answers.
func EnsureDaemon() (*Client, error) {
	// Fast path: an existing daemon answers ping.
	if c, err := Dial(); err == nil {
		if _, version, perr := c.Ping(); perr == nil {
			if version != vm.VMImplementationVersion {
				fmt.Fprintf(os.Stderr,
					"warning: homura daemon is version %d but this binary is version %d; "+
						"run `homura daemon` after stopping VMs to restart it\n",
					version, vm.VMImplementationVersion)
			}
			return c, nil
		}
	}

	// Spawn a detached daemon. Its stdio goes to the daemon log (the daemon
	// itself also points slog there); we release the process so it outlives us.
	logPath, err := DaemonLogPath()
	if err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open daemon log %s: %w", logPath, err)
	}

	exe, err := os.Executable()
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("failed to find own executable: %w", err)
	}

	cmd := exec.Command(exe, "daemon", "run")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("failed to spawn daemon: %w", err)
	}
	// Release so we don't need to reap it, and close our copy of the log fd.
	cmd.Process.Release()
	logFile.Close()

	// Poll until the daemon is listening and answering ping.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := Dial(); err == nil {
			if _, _, perr := c.Ping(); perr == nil {
				return c, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("daemon did not come up within 5s; check the log at %s", logPath)
}

// Ping calls the ping method and returns the daemon's PID and version.
func (c *Client) Ping() (pid, version int, err error) {
	var result PingResult
	if err := c.Call(MethodPing, nil, &result); err != nil {
		return 0, 0, err
	}
	return result.PID, result.Version, nil
}

// Call performs an RPC that yields a single result, ignoring any log events.
func (c *Client) Call(method string, params, result any) error {
	return c.CallStream(method, params, nil, result)
}

// CallStream performs an RPC, dispatching each "log" event line to onLog (if
// non-nil) and decoding the terminating result into result (if non-nil).
func (c *Client) CallStream(method string, params any, onLog func(line string), result any) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer conn.Close()

	var rawParams json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal params: %w", err)
		}
		rawParams = data
	}

	req := Request{Method: method, Params: rawParams, ID: 1}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	// Console/build streams can produce long lines; grow the buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var resp Response
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		switch {
		case resp.Event == "log":
			if onLog != nil {
				var le LogEvent
				if err := json.Unmarshal(resp.Data, &le); err == nil {
					onLog(le.Line)
				}
			}
		case resp.Error != nil:
			return resp.Error
		default:
			// Terminating result frame.
			if result != nil && len(resp.Result) > 0 {
				if err := json.Unmarshal(resp.Result, result); err != nil {
					return fmt.Errorf("failed to parse result: %w", err)
				}
			}
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	return fmt.Errorf("daemon closed connection without a result")
}
