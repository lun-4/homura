package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/lun-4/homura/internal/vm"
	"github.com/spf13/cobra"
)

// NinePTargetDir is set by main.go to the -d flag value
var NinePTargetDir *string

// RPCRequest represents a JSON-RPC request
type RPCRequest struct {
	Method string      `json:"method"`
	Params interface{} `json:"params"`
	ID     int         `json:"id"`
}

// RPCResponse represents a JSON-RPC response
type RPCResponse struct {
	Result interface{} `json:"result,omitempty"`
	Error  *RPCError   `json:"error,omitempty"`
	ID     int         `json:"id"`
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

// ListResult represents the result of the list method
type ListResult struct {
	Paths []string `json:"paths"`
	Count int      `json:"count"`
}

// StatusResult represents the result of the status method
type StatusResult struct {
	ExposedCount int      `json:"exposed_count"`
	ExposedPaths []string `json:"exposed_paths"`
	Uptime       string   `json:"uptime"`
	Connections  int      `json:"connections"`
}

// RequestInfo represents information about a path request
type RequestInfo struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	RequestedAt string `json:"requested_at"`
	VMPID       int    `json:"vm_pid"`
	Status      string `json:"status"`
}

// ReqListResult represents the result of req-list
type ReqListResult struct {
	Requests []RequestInfo `json:"requests"`
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

// MessageResult represents a simple message result
type MessageResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// getTargetVM finds the VM to control based on -d flag or current directory
func getTargetVM() (*vm.VMSlot, error) {
	targetDir := ""

	if NinePTargetDir != nil && *NinePTargetDir != "" {
		targetDir = *NinePTargetDir
	} else {
		// Use current directory
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current directory: %w", err)
		}
		targetDir = cwd
	}

	// Resolve to absolute path
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve directory: %w", err)
	}

	// Look up in database
	slot, err := vm.FindVMByWorkDir(absDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find VM for %s: %w", absDir, err)
	}

	return slot, nil
}

// sendNinePCommand sends a JSON-RPC command to the 9passthrough control socket
func sendNinePCommand(socketPath string, method string, params interface{}) (*RPCResponse, error) {
	// Connect to Unix socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to control socket: %w", err)
	}
	defer conn.Close()

	// Build JSON-RPC request
	request := RPCRequest{
		Method: method,
		Params: params,
		ID:     1,
	}

	// Send request
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}
		return nil, fmt.Errorf("no response from server")
	}

	// Parse response
	var response RPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

// NinePExpose exposes a host path to the VM
func NinePExpose(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	pathToExpose := args[0]

	// Convert to absolute path
	absPath, err := filepath.Abs(pathToExpose)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	params := ExposeParams{Path: absPath}
	response, err := sendNinePCommand(slot.NinePControlSocket, "expose", params)
	if err != nil {
		return fmt.Errorf("failed to expose path: %w", err)
	}

	if response.Error != nil {
		return fmt.Errorf("%s", response.Error.Message)
	}

	// Extract and print message from result
	if result, ok := response.Result.(map[string]interface{}); ok {
		if message, ok := result["message"].(string); ok {
			fmt.Println(message)
			return nil
		}
	}

	fmt.Printf("Successfully exposed: %s\n", absPath)
	return nil
}

// NinePUnexpose removes a path from the exposed set
func NinePUnexpose(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	pathToUnexpose := args[0]

	// Convert to absolute path
	absPath, err := filepath.Abs(pathToUnexpose)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	params := UnexposeParams{Path: absPath}
	response, err := sendNinePCommand(slot.NinePControlSocket, "unexpose", params)
	if err != nil {
		return fmt.Errorf("failed to unexpose path: %w", err)
	}

	if response.Error != nil {
		return fmt.Errorf("%s", response.Error.Message)
	}

	// Extract and print message from result
	if result, ok := response.Result.(map[string]interface{}); ok {
		if message, ok := result["message"].(string); ok {
			fmt.Println(message)
			return nil
		}
	}

	fmt.Printf("Successfully unexposed: %s\n", absPath)
	return nil
}

// NinePList lists all exposed paths
func NinePList(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	response, err := sendNinePCommand(slot.NinePControlSocket, "list", nil)
	if err != nil {
		return fmt.Errorf("failed to list paths: %w", err)
	}

	if response.Error != nil {
		return fmt.Errorf("%s", response.Error.Message)
	}

	// Parse result
	resultData, err := json.Marshal(response.Result)
	if err != nil {
		return fmt.Errorf("failed to process result: %w", err)
	}

	var result ListResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return fmt.Errorf("failed to parse result: %w", err)
	}

	if len(result.Paths) == 0 {
		fmt.Println("No paths exposed")
		return nil
	}

	fmt.Printf("Exposed paths (%d):\n", result.Count)
	for _, path := range result.Paths {
		fmt.Printf("  %s\n", path)
	}

	return nil
}

// NinePStatus shows 9p server status
func NinePStatus(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	response, err := sendNinePCommand(slot.NinePControlSocket, "status", nil)
	if err != nil {
		return fmt.Errorf("failed to get status: %w", err)
	}

	if response.Error != nil {
		return fmt.Errorf("%s", response.Error.Message)
	}

	// Parse result
	resultData, err := json.Marshal(response.Result)
	if err != nil {
		return fmt.Errorf("failed to process result: %w", err)
	}

	var result StatusResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return fmt.Errorf("failed to parse result: %w", err)
	}

	fmt.Println("Server Status:")
	fmt.Printf("  Uptime:         %s\n", result.Uptime)
	fmt.Printf("  Connections:    %d\n", result.Connections)
	fmt.Printf("  Exposed Paths:  %d\n", result.ExposedCount)

	if len(result.ExposedPaths) > 0 {
		fmt.Println("\nExposed paths:")
		for _, path := range result.ExposedPaths {
			fmt.Printf("  %s\n", path)
		}
	}

	return nil
}

// NinePReqList lists pending path requests
func NinePReqList(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	response, err := sendNinePCommand(slot.NinePControlSocket, "req-list", nil)
	if err != nil {
		return fmt.Errorf("failed to list requests: %w", err)
	}

	if response.Error != nil {
		return fmt.Errorf("%s", response.Error.Message)
	}

	// Parse result
	resultData, err := json.Marshal(response.Result)
	if err != nil {
		return fmt.Errorf("failed to process result: %w", err)
	}

	var result ReqListResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return fmt.Errorf("failed to parse result: %w", err)
	}

	if len(result.Requests) == 0 {
		fmt.Println("No pending requests")
		return nil
	}

	fmt.Printf("Pending requests (%d):\n", len(result.Requests))
	for _, req := range result.Requests {
		fmt.Printf("  [%s] %s (pid %d, requested at %s)\n",
			req.ID, req.Path, req.VMPID, req.RequestedAt)
	}

	return nil
}

// NinePReqApprove approves a VM path request
func NinePReqApprove(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	requestID := args[0]

	params := ReqApproveParams{RequestID: requestID}
	response, err := sendNinePCommand(slot.NinePControlSocket, "req-approve", params)
	if err != nil {
		return fmt.Errorf("failed to approve request: %w", err)
	}

	if response.Error != nil {
		return fmt.Errorf("%s", response.Error.Message)
	}

	// Parse result
	resultData, err := json.Marshal(response.Result)
	if err != nil {
		return fmt.Errorf("failed to process result: %w", err)
	}

	var result MessageResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return fmt.Errorf("failed to parse result: %w", err)
	}

	fmt.Println(result.Message)
	return nil
}

// NinePReqDeny denies a VM path request
func NinePReqDeny(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	requestID := args[0]
	reason := ""
	if len(args) > 1 {
		reason = args[1]
	}

	params := ReqDenyParams{
		RequestID: requestID,
		Reason:    reason,
	}
	response, err := sendNinePCommand(slot.NinePControlSocket, "req-deny", params)
	if err != nil {
		return fmt.Errorf("failed to deny request: %w", err)
	}

	if response.Error != nil {
		return fmt.Errorf("%s", response.Error.Message)
	}

	// Parse result
	resultData, err := json.Marshal(response.Result)
	if err != nil {
		return fmt.Errorf("failed to process result: %w", err)
	}

	var result MessageResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return fmt.Errorf("failed to parse result: %w", err)
	}

	fmt.Println(result.Message)
	return nil
}
