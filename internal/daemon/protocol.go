package daemon

import "encoding/json"

// The daemon speaks newline-delimited JSON over a unix socket, one connection
// per request. The framing mirrors the 9passthrough control protocol
// (internal/commands/9p.go), extended with streaming "log" event frames so
// build/boot progress can flow to the client before the final result.
//
//	client → daemon:  {"method": "...", "params": {...}, "id": 1}\n
//	daemon → client:  0+ × {"event": "log", "id": 1, "data": {"line": "..."}}\n
//	                  then 1 × {"result": {...}, "id": 1} | {"error": {...}, "id": 1}\n

// Request is a single client→daemon call.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     int             `json:"id"`
}

// Response is a daemon→client frame. Exactly one of Event, Result, or Error is
// set. Event frames may repeat; a Result or Error frame terminates the stream.
type Response struct {
	Event  string          `json:"event,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
	ID     int             `json:"id"`
}

// Error is a structured RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// LogEvent is the payload of an {"event":"log"} frame.
type LogEvent struct {
	Line string `json:"line"`
}

// Error codes.
const (
	CodeInvalidRequest = 1
	CodeInternal       = 2
	CodeUnknownVM      = 3
)

// Method names.
const (
	MethodPing     = "ping"
	MethodStartVM  = "start-vm"
	MethodStopVM   = "stop-vm"
	MethodListVMs  = "list-vms"
	MethodShutdown = "shutdown"
)

// PingResult is returned by the ping method.
type PingResult struct {
	PID     int `json:"pid"`
	Version int `json:"version"`
}

// StartVMParams are the parameters for the start-vm method.
type StartVMParams struct {
	WorkDir      string `json:"work_dir"`
	ShareMode    string `json:"share_mode"`
	StateDirBase string `json:"state_dir_base,omitempty"`
}

// VMResult describes a running daemon-owned VM. It is used as the start-vm
// result and as each entry of the list-vms result.
type VMResult struct {
	Slot          int    `json:"slot"`
	IP            string `json:"ip"`
	SSHPort       int    `json:"ssh_port"`
	PortStart     int    `json:"port_start"`
	PortEnd       int    `json:"port_end"`
	ConsoleSocket string `json:"console_socket"`
	ConsoleLog    string `json:"console_log"`
	QEMUPID       int    `json:"qemu_pid"`
	ShareMode     string `json:"share_mode"`
}

// StopVMParams are the parameters for the stop-vm method.
type StopVMParams struct {
	Slot int `json:"slot"`
}

// StopVMResult is returned by the stop-vm method.
type StopVMResult struct {
	Slot       int    `json:"slot"`
	ConsoleLog string `json:"console_log"`
}

// ListVMsResult is returned by the list-vms method.
type ListVMsResult struct {
	VMs []VMResult `json:"vms"`
}

// ShutdownResult is returned by the shutdown method.
type ShutdownResult struct {
	Stopped int `json:"stopped"`
}
