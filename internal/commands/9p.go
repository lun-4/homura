package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lun-4/homura/internal/vm"
	"github.com/spf13/cobra"
)

// NinePTargetDir is set by main.go to the -d flag value
var NinePTargetDir *string

// NinePTargetPID is set by main.go to the -p flag value
var NinePTargetPID *int

// NinePTargetVMID is set by main.go to the -vm flag value
var NinePTargetVMID *int

// ShareInfo represents a share in the virtiofsd API
type ShareInfo struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
	Mode string `json:"mode"` // "rw" or "ro"
}

// RequestInfo represents information about a path request
type RequestInfo struct {
	ID     int    `json:"id"`
	Path   string `json:"path"`
	Mode   string `json:"mode"`   // "rw" or "ro"
	Status string `json:"status"` // "pending", "approved", "denied"
}

// MessageResult represents a simple message result
type MessageResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ShareController is the interface for controlling filesystem sharing backends
type ShareController interface {
	Expose(path string, readOnly bool) (*MessageResult, error)
	Unexpose(path string) (*MessageResult, error)
	List() ([]ShareInfo, error)
	ListRequests() ([]RequestInfo, error)
	ApproveRequest(id int) (*MessageResult, error)
	DenyRequest(id int) (*MessageResult, error)
}

// ============================================================================
// 9passthrough Controller (JSON-RPC over Unix socket)
// ============================================================================

// NinePController implements ShareController for 9passthrough
type NinePController struct {
	SocketPath string
}

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

// sendCommand sends a JSON-RPC command to the 9passthrough control socket
func (c *NinePController) sendCommand(method string, params interface{}) (*RPCResponse, error) {
	conn, err := net.Dial("unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to control socket: %w", err)
	}
	defer conn.Close()

	request := RPCRequest{
		Method: method,
		Params: params,
		ID:     1,
	}

	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}
		return nil, fmt.Errorf("no response from server")
	}

	var response RPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

func (c *NinePController) Expose(path string, readOnly bool) (*MessageResult, error) {
	params := map[string]interface{}{"path": path, "readonly": readOnly}
	resp, err := c.sendCommand("expose", params)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	return &MessageResult{Success: true, Message: fmt.Sprintf("Exposed: %s", path)}, nil
}

func (c *NinePController) Unexpose(path string) (*MessageResult, error) {
	params := map[string]interface{}{"path": path}
	resp, err := c.sendCommand("unexpose", params)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	return &MessageResult{Success: true, Message: fmt.Sprintf("Unexposed: %s", path)}, nil
}

func (c *NinePController) List() ([]ShareInfo, error) {
	resp, err := c.sendCommand("list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}

	resultData, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to process result: %w", err)
	}

	var result []ShareInfo
	if err := json.Unmarshal(resultData, &result); err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}

	return result, nil
}

func (c *NinePController) ListRequests() ([]RequestInfo, error) {
	resp, err := c.sendCommand("req-list", nil)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}

	resultData, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to process result: %w", err)
	}

	var result []RequestInfo
	if err := json.Unmarshal(resultData, &result); err != nil {
		return nil, fmt.Errorf("failed to parse result: %w", err)
	}

	return result, nil
}

func (c *NinePController) ApproveRequest(id int) (*MessageResult, error) {
	params := map[string]any{"request_id": id}
	resp, err := c.sendCommand("req-approve", params)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	return &MessageResult{Success: true, Message: fmt.Sprintf("Request %d approved", id)}, nil
}

func (c *NinePController) DenyRequest(id int) (*MessageResult, error) {
	params := map[string]any{"request_id": id}
	resp, err := c.sendCommand("req-deny", params)
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	return &MessageResult{Success: true, Message: fmt.Sprintf("Request %d denied", id)}, nil
}

// ============================================================================
// Virtiofs Controller (HTTP REST API)
// ============================================================================

// VirtiofsController implements ShareController for virtiofsd HTTP API
type VirtiofsController struct {
	AdminPort int
	Token     string
}

func (c *VirtiofsController) doRequest(method, endpoint string, body interface{}) ([]byte, error) {
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", c.AdminPort)
	reqURL := baseURL + endpoint

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server error: %s - %s", resp.Status, string(respBody))
	}

	return respBody, nil
}

func (c *VirtiofsController) Expose(path string, readOnly bool) (*MessageResult, error) {
	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	body := map[string]interface{}{"path": path, "mode": mode}
	_, err := c.doRequest("POST", "/shares", body)
	if err != nil {
		return nil, err
	}
	return &MessageResult{Success: true, Message: fmt.Sprintf("Exposed: %s", path)}, nil
}

func (c *VirtiofsController) Unexpose(path string) (*MessageResult, error) {
	endpoint := "/shares?path=" + url.QueryEscape(path)
	_, err := c.doRequest("DELETE", endpoint, nil)
	if err != nil {
		return nil, err
	}
	return &MessageResult{Success: true, Message: fmt.Sprintf("Unexposed: %s", path)}, nil
}

func (c *VirtiofsController) List() ([]ShareInfo, error) {
	respBody, err := c.doRequest("GET", "/shares", nil)
	if err != nil {
		return nil, err
	}

	var result []ShareInfo
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

func (c *VirtiofsController) ListRequests() ([]RequestInfo, error) {
	respBody, err := c.doRequest("GET", "/pending-requests", nil)
	if err != nil {
		return nil, err
	}

	var result []RequestInfo
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

func (c *VirtiofsController) ApproveRequest(id int) (*MessageResult, error) {
	endpoint := fmt.Sprintf("/pending-requests/%d/approve", id)
	_, err := c.doRequest("POST", endpoint, nil)
	if err != nil {
		return nil, err
	}
	return &MessageResult{Success: true, Message: fmt.Sprintf("Request %d approved", id)}, nil
}

func (c *VirtiofsController) DenyRequest(id int) (*MessageResult, error) {
	endpoint := fmt.Sprintf("/pending-requests/%d/deny", id)
	_, err := c.doRequest("POST", endpoint, nil)
	if err != nil {
		return nil, err
	}
	return &MessageResult{Success: true, Message: fmt.Sprintf("Request %d denied", id)}, nil
}

// ============================================================================
// Helper functions
// ============================================================================

// getTargetVM finds the VM to control based on -vm flag, -p flag, -d flag, or current directory
func getTargetVM() (*vm.VMSlot, error) {
	// Check for VM slot ID lookup first
	if NinePTargetVMID != nil && *NinePTargetVMID != 0 {
		slot, err := vm.FindVMBySlot(*NinePTargetVMID)
		if err != nil {
			return nil, fmt.Errorf("failed to find VM with slot %d: %w", *NinePTargetVMID, err)
		}
		return slot, nil
	}

	// Check for PID-based lookup
	if NinePTargetPID != nil && *NinePTargetPID != 0 {
		slot, err := vm.FindVMByNinePPID(*NinePTargetPID)
		if err != nil {
			return nil, fmt.Errorf("failed to find VM for 9passthrough PID %d: %w", *NinePTargetPID, err)
		}
		return slot, nil
	}

	// Fall back to directory-based lookup
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

	// Look up all VMs for this directory
	slots, err := vm.FindVMsByWorkDir(absDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find VMs: %w", err)
	}

	if len(slots) == 0 {
		return nil, fmt.Errorf("no VM found for %s", absDir)
	}

	if len(slots) == 1 {
		return slots[0], nil
	}

	// Multiple VMs - show selection list
	fmt.Printf("Multiple VMs found for %s:\n\n", absDir)
	for i, s := range slots {
		fmt.Printf("  %d) slot %d - %s:%d (created %s)\n", i+1, s.SlotNumber, s.IPAddress, s.PortStart, formatRelativeTime(s.CreatedAt))
	}
	fmt.Printf("\nSelect VM [1-%d]: ", len(slots))

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	choice, err := strconv.Atoi(input)
	if err != nil || choice < 1 || choice > len(slots) {
		return nil, fmt.Errorf("invalid selection: %s", input)
	}

	return slots[choice-1], nil
}

// getShareController returns the appropriate ShareController for the VM's share mode
func getShareController(slot *vm.VMSlot) (ShareController, error) {
	switch slot.ShareMode {
	case "virtiofs":
		// Read token from file
		tokenFile := fmt.Sprintf("/tmp/virtiofs-token-%d", slot.VirtiofsPID)
		tokenData, err := os.ReadFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read virtiofs token file: %w", err)
		}
		lines := strings.Split(strings.TrimSpace(string(tokenData)), "\n")
		if len(lines) < 2 {
			return nil, fmt.Errorf("invalid virtiofs token file format")
		}
		token := lines[0]
		adminPort, err := strconv.Atoi(lines[1])
		if err != nil {
			return nil, fmt.Errorf("invalid admin port in token file: %w", err)
		}
		return &VirtiofsController{AdminPort: adminPort, Token: token}, nil

	default: // "9p" or empty
		return &NinePController{SocketPath: slot.NinePControlSocket}, nil
	}
}

// ============================================================================
// Command handlers
// ============================================================================

// NinePExpose exposes a host path to the VM
func NinePExpose(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	controller, err := getShareController(slot)
	if err != nil {
		return err
	}

	pathToExpose := args[0]

	// Check for read-only flag
	readOnly := false
	if len(args) > 1 && args[1] == "ro" {
		readOnly = true
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(pathToExpose)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	result, err := controller.Expose(absPath, readOnly)
	if err != nil {
		return fmt.Errorf("failed to expose path: %w", err)
	}

	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	fmt.Printf("Successfully exposed: %s (%s)\n", absPath, mode)
	if result.Message != "" && result.Message != fmt.Sprintf("Exposed: %s", absPath) {
		fmt.Println(result.Message)
	}
	return nil
}

// NinePUnexpose removes a path from the exposed set
func NinePUnexpose(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	controller, err := getShareController(slot)
	if err != nil {
		return err
	}

	pathToUnexpose := args[0]

	// Convert to absolute path
	absPath, err := filepath.Abs(pathToUnexpose)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	_, err = controller.Unexpose(absPath)
	if err != nil {
		return fmt.Errorf("failed to unexpose path: %w", err)
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

	controller, err := getShareController(slot)
	if err != nil {
		return err
	}

	shares, err := controller.List()
	if err != nil {
		return fmt.Errorf("failed to list paths: %w", err)
	}

	if len(shares) == 0 {
		fmt.Println("No paths exposed")
		return nil
	}

	fmt.Printf("Exposed paths (%d):\n", len(shares))
	for _, share := range shares {
		fmt.Printf("  %s (%s)\n", share.Path, share.Mode)
	}

	return nil
}

// NinePReqList lists pending path requests
func NinePReqList(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	controller, err := getShareController(slot)
	if err != nil {
		return err
	}

	requests, err := controller.ListRequests()
	if err != nil {
		return fmt.Errorf("failed to list requests: %w", err)
	}

	if len(requests) == 0 {
		fmt.Println("No pending requests")
		return nil
	}

	fmt.Printf("Pending requests (%d):\n", len(requests))
	for _, req := range requests {
		fmt.Printf("  [%d] %s (%s) - %s\n", req.ID, req.Path, req.Mode, req.Status)
	}

	return nil
}

// NinePReqApprove approves a VM path request
func NinePReqApprove(cmd *cobra.Command, args []string) error {
	slot, err := getTargetVM()
	if err != nil {
		return err
	}

	controller, err := getShareController(slot)
	if err != nil {
		return err
	}

	requestID, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid request ID: %w", err)
	}

	result, err := controller.ApproveRequest(requestID)
	if err != nil {
		return fmt.Errorf("failed to approve request: %w", err)
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

	controller, err := getShareController(slot)
	if err != nil {
		return err
	}

	requestID, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid request ID: %w", err)
	}

	result, err := controller.DenyRequest(requestID)
	if err != nil {
		return fmt.Errorf("failed to deny request: %w", err)
	}

	fmt.Println(result.Message)
	return nil
}
