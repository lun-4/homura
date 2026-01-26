package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

// RPCRequest represents a JSON-RPC request
type RPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	ID     interface{}     `json:"id"`
}

// RPCResponse represents a JSON-RPC response
type RPCResponse struct {
	Result interface{} `json:"result,omitempty"`
	Error  *RPCError   `json:"error,omitempty"`
	ID     interface{} `json:"id"`
}

// RPCError represents a JSON-RPC error
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ExposeParams represents parameters for the expose method
type ExposeParams struct {
	Path string `json:"path"`
}

// UnexposeParams represents parameters for the unexpose method
type UnexposeParams struct {
	Path string `json:"path"`
}

// RequestParams represents parameters for the request method
type RequestParams struct {
	Path  string `json:"path"`
	Token string `json:"token"`
}

// ReqApproveParams represents parameters for req-approve
type ReqApproveParams struct {
	RequestID string `json:"request_id"`
}

// ReqDenyParams represents parameters for req-deny
type ReqDenyParams struct {
	RequestID string `json:"request_id"`
	Reason    string `json:"reason,omitempty"`
}

// StatusResult represents the result of the status method
type StatusResult struct {
	ExposedCount int      `json:"exposed_count"`
	ExposedPaths []string `json:"exposed_paths"`
	Uptime       string   `json:"uptime"`
	Connections  int      `json:"connections"`
}

// ControlServer manages the control socket server
type ControlServer struct {
	socketPath   string
	tcpPort      int
	registry     *PathRegistry
	requestQueue *RequestQueue
	listener     net.Listener
	startTime    time.Time
	connections  int
	mu           sync.Mutex
	authToken    string
	isUnixSocket bool
	vmPID        int
}

// NewUnixControlServer creates a new Unix socket control server
func NewUnixControlServer(socketPath string, registry *PathRegistry, requestQueue *RequestQueue, vmPID int) *ControlServer {
	return &ControlServer{
		socketPath:   socketPath,
		registry:     registry,
		requestQueue: requestQueue,
		startTime:    time.Now(),
		isUnixSocket: true,
		vmPID:        vmPID,
	}
}

// NewTCPControlServer creates a new TCP control server with authentication
func NewTCPControlServer(port int, authToken string, registry *PathRegistry, requestQueue *RequestQueue, vmPID int) *ControlServer {
	return &ControlServer{
		tcpPort:      port,
		registry:     registry,
		requestQueue: requestQueue,
		startTime:    time.Now(),
		authToken:    authToken,
		isUnixSocket: false,
		vmPID:        vmPID,
	}
}

// Start starts the control server
func (cs *ControlServer) Start() error {
	var listener net.Listener
	var err error

	if cs.isUnixSocket {
		// Remove existing socket if it exists
		os.Remove(cs.socketPath)

		// Create Unix socket listener
		listener, err = net.Listen("unix", cs.socketPath)
		if err != nil {
			return fmt.Errorf("failed to create control socket: %w", err)
		}

		// Set socket permissions to 0600 (owner only)
		if err := os.Chmod(cs.socketPath, 0600); err != nil {
			listener.Close()
			return fmt.Errorf("failed to set socket permissions: %w", err)
		}

		log.Printf("Control server listening on Unix socket: %s", cs.socketPath)
	} else {
		// Create TCP listener
		listener, err = net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", cs.tcpPort))
		if err != nil {
			return fmt.Errorf("failed to create TCP control socket: %w", err)
		}

		log.Printf("Control server listening on TCP port: %d", cs.tcpPort)
	}

	cs.listener = listener

	// Accept connections in a goroutine
	go cs.acceptLoop()

	return nil
}

// acceptLoop accepts and handles connections
func (cs *ControlServer) acceptLoop() {
	for {
		conn, err := cs.listener.Accept()
		if err != nil {
			// Check if listener was closed
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				return
			}
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		// Handle connection in a goroutine
		go cs.handleConnection(conn)
	}
}

// handleConnection handles a single client connection
func (cs *ControlServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	cs.mu.Lock()
	cs.connections++
	cs.mu.Unlock()

	// Read and handle requests
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()

		// Parse JSON-RPC request
		var req RPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			response := RPCResponse{
				Error: &RPCError{
					Code:    -32700,
					Message: fmt.Sprintf("Parse error: %v", err),
				},
				ID: nil,
			}
			cs.sendResponse(conn, response)
			continue
		}

		// Handle request and send response
		response := cs.handleRequest(req)
		cs.sendResponse(conn, response)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading from connection: %v", err)
	}
}

// authenticateRequest checks if the request has a valid token (TCP only)
func (cs *ControlServer) authenticateRequest(req RPCRequest) error {
	// Unix socket connections are implicitly authenticated by filesystem permissions
	if cs.isUnixSocket {
		return nil
	}

	// For TCP connections, require token authentication
	// Only the "request" method requires token (it comes from the VM)
	if req.Method != "request" {
		// Other methods should only be called from Unix socket
		return fmt.Errorf("method %s not available on TCP socket", req.Method)
	}

	// Extract token from params
	var params RequestParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return fmt.Errorf("invalid params")
	}

	if params.Token != cs.authToken {
		return fmt.Errorf("invalid authentication token")
	}

	return nil
}

// handleRequest handles a JSON-RPC request
func (cs *ControlServer) handleRequest(req RPCRequest) RPCResponse {
	// Authenticate request
	if err := cs.authenticateRequest(req); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32600,
				Message: fmt.Sprintf("Authentication failed: %v", err),
			},
			ID: req.ID,
		}
	}

	switch req.Method {
	case "expose":
		return cs.handleExpose(req)
	case "unexpose":
		return cs.handleUnexpose(req)
	case "list":
		return cs.handleList(req)
	case "status":
		return cs.handleStatus(req)
	case "request":
		return cs.handleRequestMethod(req)
	case "req-list":
		return cs.handleReqList(req)
	case "req-approve":
		return cs.handleReqApprove(req)
	case "req-deny":
		return cs.handleReqDeny(req)
	default:
		return RPCResponse{
			Error: &RPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
			ID: req.ID,
		}
	}
}

// handleExpose handles the expose method
func (cs *ControlServer) handleExpose(req RPCRequest) RPCResponse {
	var params ExposeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
			ID: req.ID,
		}
	}

	if params.Path == "" {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: "Path parameter is required",
			},
			ID: req.ID,
		}
	}

	// Add path to registry
	if err := cs.registry.AddPath(params.Path); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32000,
				Message: fmt.Sprintf("Failed to expose path: %v", err),
			},
			ID: req.ID,
		}
	}

	return RPCResponse{
		Result: map[string]interface{}{
			"success": true,
			"path":    params.Path,
			"message": fmt.Sprintf("Successfully exposed: %s", params.Path),
		},
		ID: req.ID,
	}
}

// handleUnexpose handles the unexpose method
func (cs *ControlServer) handleUnexpose(req RPCRequest) RPCResponse {
	var params UnexposeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
			ID: req.ID,
		}
	}

	if params.Path == "" {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: "Path parameter is required",
			},
			ID: req.ID,
		}
	}

	// Remove path from registry
	cs.registry.RemovePath(params.Path)

	return RPCResponse{
		Result: map[string]interface{}{
			"success": true,
			"path":    params.Path,
			"message": fmt.Sprintf("Successfully unexposed: %s", params.Path),
		},
		ID: req.ID,
	}
}

// handleList handles the list method
func (cs *ControlServer) handleList(req RPCRequest) RPCResponse {
	paths := cs.registry.ListPaths()

	return RPCResponse{
		Result: map[string]interface{}{
			"paths": paths,
			"count": len(paths),
		},
		ID: req.ID,
	}
}

// handleStatus handles the status method
func (cs *ControlServer) handleStatus(req RPCRequest) RPCResponse {
	cs.mu.Lock()
	connections := cs.connections
	cs.mu.Unlock()

	paths := cs.registry.ListPaths()
	uptime := time.Since(cs.startTime)

	return RPCResponse{
		Result: StatusResult{
			ExposedCount: len(paths),
			ExposedPaths: paths,
			Uptime:       uptime.String(),
			Connections:  connections,
		},
		ID: req.ID,
	}
}

// handleRequestMethod handles the request method (VM requesting a path)
func (cs *ControlServer) handleRequestMethod(req RPCRequest) RPCResponse {
	var params RequestParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
			ID: req.ID,
		}
	}

	if params.Path == "" {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: "Path parameter is required",
			},
			ID: req.ID,
		}
	}

	// Create path request
	pathReq := cs.requestQueue.Create(params.Path, cs.vmPID)

	// Send desktop notification
	cs.notifyRequest(pathReq)

	log.Printf("Path request created: %s for %s", pathReq.ID, pathReq.Path)

	// Block and wait for approval/denial
	ctx := context.Background()
	result := pathReq.Wait(ctx)

	if result.Approved {
		// Expose the path
		if err := cs.registry.AddPath(params.Path); err != nil {
			return RPCResponse{
				Error: &RPCError{
					Code:    -32000,
					Message: fmt.Sprintf("Failed to expose path: %v", err),
				},
				ID: req.ID,
			}
		}

		return RPCResponse{
			Result: map[string]interface{}{
				"approved": true,
				"path":     params.Path,
			},
			ID: req.ID,
		}
	}

	// Request was denied
	return RPCResponse{
		Error: &RPCError{
			Code:    -32001,
			Message: fmt.Sprintf("Request denied: %s", result.Reason),
		},
		ID: req.ID,
	}
}

// handleReqList handles the req-list method
func (cs *ControlServer) handleReqList(req RPCRequest) RPCResponse {
	requests := cs.requestQueue.List()

	type RequestInfo struct {
		ID          string `json:"id"`
		Path        string `json:"path"`
		RequestedAt string `json:"requested_at"`
		VMPID       int    `json:"vm_pid"`
		Status      string `json:"status"`
	}

	result := make([]RequestInfo, 0, len(requests))
	for _, r := range requests {
		result = append(result, RequestInfo{
			ID:          r.ID,
			Path:        r.Path,
			RequestedAt: r.RequestedAt.Format(time.RFC3339),
			VMPID:       r.VMPID,
			Status:      string(r.Status),
		})
	}

	return RPCResponse{
		Result: map[string]interface{}{
			"requests": result,
		},
		ID: req.ID,
	}
}

// handleReqApprove handles the req-approve method
func (cs *ControlServer) handleReqApprove(req RPCRequest) RPCResponse {
	var params ReqApproveParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
			ID: req.ID,
		}
	}

	if params.RequestID == "" {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: "request_id parameter is required",
			},
			ID: req.ID,
		}
	}

	// Approve the request
	if err := cs.requestQueue.Approve(params.RequestID); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32000,
				Message: fmt.Sprintf("Failed to approve request: %v", err),
			},
			ID: req.ID,
		}
	}

	return RPCResponse{
		Result: map[string]interface{}{
			"success": true,
			"message": "Request approved and path exposed",
		},
		ID: req.ID,
	}
}

// handleReqDeny handles the req-deny method
func (cs *ControlServer) handleReqDeny(req RPCRequest) RPCResponse {
	var params ReqDenyParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
			ID: req.ID,
		}
	}

	if params.RequestID == "" {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32602,
				Message: "request_id parameter is required",
			},
			ID: req.ID,
		}
	}

	// Deny the request
	reason := params.Reason
	if reason == "" {
		reason = "Denied by user"
	}

	if err := cs.requestQueue.Deny(params.RequestID, reason); err != nil {
		return RPCResponse{
			Error: &RPCError{
				Code:    -32000,
				Message: fmt.Sprintf("Failed to deny request: %v", err),
			},
			ID: req.ID,
		}
	}

	return RPCResponse{
		Result: map[string]interface{}{
			"success": true,
			"message": "Request denied",
		},
		ID: req.ID,
	}
}

// notifyRequest sends a desktop notification for a path request
func (cs *ControlServer) notifyRequest(req *PathRequest) {
	msg := fmt.Sprintf("Request #%s: [pid %d] wants %s", req.ID, req.VMPID, req.Path)

	cmd := exec.Command("notify-send", "-u", "critical", "9p Path Request", msg)
	cmd.Env = os.Environ()

	// Run in background, ignore errors
	go func() {
		if err := cmd.Run(); err != nil {
			log.Printf("Warning: Failed to send notification: %v", err)
		}
	}()
}

// sendResponse sends a JSON-RPC response to the client
func (cs *ControlServer) sendResponse(conn net.Conn, response RPCResponse) {
	data, err := json.Marshal(response)
	if err != nil {
		log.Printf("Error marshaling response: %v", err)
		return
	}

	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		log.Printf("Error writing response: %v", err)
	}
}

// Stop stops the control server
func (cs *ControlServer) Stop() error {
	if cs.listener != nil {
		if err := cs.listener.Close(); err != nil {
			return err
		}
	}

	// Remove socket file
	if err := os.Remove(cs.socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}
