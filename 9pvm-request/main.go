package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

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

// RequestParams represents parameters for the request method
type RequestParams struct {
	Path     string `json:"path"`
	Token    string `json:"token"`
	ReadOnly bool   `json:"readonly,omitempty"`
}

// RequestResult represents the result of a successful request
type RequestResult struct {
	Approved bool   `json:"approved"`
	Path     string `json:"path"`
}

// VirtiofsRequest represents a share request for virtiofsd
type VirtiofsRequest struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
}

// VirtiofsResponse represents the response from virtiofsd
type VirtiofsResponse struct {
	ID         int    `json:"id"`
	Path       string `json:"path"`
	Mode       string `json:"mode"`
	Status     string `json:"status"`
	DenyReason string `json:"deny_reason,omitempty"`
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <path> [ro]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nRequest a host path to be exposed by the filesystem server.\n")
	fmt.Fprintf(os.Stderr, "The path must be an absolute path on the HOST filesystem.\n")
	fmt.Fprintf(os.Stderr, "\nIf the path starts with /mnt/host/, that prefix is stripped automatically.\n")
	fmt.Fprintf(os.Stderr, "Add 'ro' to request read-only access.\n")
	fmt.Fprintf(os.Stderr, "\nExamples:\n")
	fmt.Fprintf(os.Stderr, "  %s /home/luna/projects      # Request host path directly\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s /mnt/host/home/luna/foo  # Equivalent to /home/luna/foo\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s /home/luna/data ro       # Request read-only access\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nThis command blocks until the request is approved or denied.\n")
}

func main() {
	// Handle help flags
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		printUsage()
		if len(os.Args) >= 2 {
			os.Exit(0) // Explicit help request
		}
		os.Exit(1) // No args provided
	}

	// Get path argument
	path := os.Args[1]

	// Require absolute paths
	if !strings.HasPrefix(path, "/") {
		fmt.Fprintf(os.Stderr, "Error: path must be absolute (start with /)\n")
		fmt.Fprintf(os.Stderr, "Got: %s\n\n", path)
		printUsage()
		os.Exit(1)
	}

	// Strip /mnt/host prefix if present (user might tab-complete from inside VM)
	const mountPrefix = "/mnt/host"
	if strings.HasPrefix(path, mountPrefix+"/") {
		path = strings.TrimPrefix(path, mountPrefix)
	} else if path == mountPrefix {
		fmt.Fprintf(os.Stderr, "Error: cannot request /mnt/host itself\n")
		os.Exit(1)
	}

	// Check for read-only flag
	readOnly := false
	if len(os.Args) > 2 && os.Args[2] == "ro" {
		readOnly = true
	}

	// Read kernel cmdline to get share mode, token and port
	shareMode, token, port, err := readKernelParams()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nThis command must be run inside a VM with filesystem sharing configured.\n")
		os.Exit(1)
	}

	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	fmt.Printf("Requesting path: %s (%s)\n", path, mode)
	fmt.Println("Waiting for host approval...")

	// Dispatch based on share mode
	switch shareMode {
	case "virtiofs":
		if err := requestViaVirtiofs(path, readOnly, token, port); err != nil {
			fmt.Fprintf(os.Stderr, "Request failed: %v\n", err)
			os.Exit(1)
		}
	default: // "9p"
		if err := requestVia9p(path, readOnly, token, port); err != nil {
			fmt.Fprintf(os.Stderr, "Request failed: %v\n", err)
			os.Exit(1)
		}
	}
}

// requestViaVirtiofs sends a share request via HTTP to virtiofsd
func requestViaVirtiofs(path string, readOnly bool, token, port string) error {
	mode := "rw"
	if readOnly {
		mode = "ro"
	}

	reqBody := VirtiofsRequest{Path: path, Mode: mode}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("http://10.0.2.2:%s/request-share-blocking", port)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to host: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("server error: %s - %s", resp.Status, string(respBody))
	}

	var result VirtiofsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	switch result.Status {
	case "approved":
		fmt.Printf("✓ Request approved! Path exposed: %s\n", result.Path)
		return nil
	case "denied":
		if result.DenyReason != "" {
			return fmt.Errorf("request denied: %s", result.DenyReason)
		}
		return fmt.Errorf("request denied")
	default:
		return fmt.Errorf("unexpected status: %s", result.Status)
	}
}

// requestVia9p sends a share request via JSON-RPC to 9passthrough
func requestVia9p(path string, readOnly bool, token, port string) error {
	// Connect to control server
	hostAddr := fmt.Sprintf("10.0.2.2:%s", port)
	conn, err := net.Dial("tcp", hostAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to host control server: %w", err)
	}
	defer conn.Close()

	// Build request
	params := RequestParams{
		Path:     path,
		Token:    token,
		ReadOnly: readOnly,
	}

	request := RPCRequest{
		Method: "request",
		Params: params,
		ID:     1,
	}

	// Send request
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	// Read response (this blocks until approved/denied)
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}
		return fmt.Errorf("connection closed by host")
	}

	// Parse response
	var response RPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for error
	if response.Error != nil {
		return fmt.Errorf("request denied: %s", response.Error.Message)
	}

	// Parse result
	resultData, err := json.Marshal(response.Result)
	if err != nil {
		return fmt.Errorf("failed to process result: %w", err)
	}

	var result RequestResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		return fmt.Errorf("failed to parse result: %w", err)
	}

	if result.Approved {
		fmt.Printf("✓ Request approved! Path exposed: %s\n", result.Path)
		return nil
	}

	return fmt.Errorf("request denied")
}

// readKernelParams reads share mode, token and port from /proc/cmdline
// Returns shareMode ("virtiofs" or "9p"), token, port
func readKernelParams() (shareMode, token, port string, err error) {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return "", "", "", fmt.Errorf("failed to read /proc/cmdline: %w", err)
	}

	cmdline := string(data)
	parts := strings.Fields(cmdline)

	// Try virtiofs params first
	var virtiofsToken, virtiofsPort string
	var p9Token, p9Port string

	for _, part := range parts {
		switch {
		case strings.HasPrefix(part, "virtiofs.token="):
			virtiofsToken = strings.TrimPrefix(part, "virtiofs.token=")
		case strings.HasPrefix(part, "virtiofs.port="):
			virtiofsPort = strings.TrimPrefix(part, "virtiofs.port=")
		case strings.HasPrefix(part, "p9.token="):
			p9Token = strings.TrimPrefix(part, "p9.token=")
		case strings.HasPrefix(part, "p9.port="):
			p9Port = strings.TrimPrefix(part, "p9.port=")
		}
	}

	// Check for virtiofs mode first
	if virtiofsToken != "" && virtiofsPort != "" {
		return "virtiofs", virtiofsToken, virtiofsPort, nil
	}

	// Fall back to 9p mode
	if p9Token != "" && p9Port != "" {
		return "9p", p9Token, p9Port, nil
	}

	// Neither mode found
	if virtiofsToken != "" || virtiofsPort != "" {
		return "", "", "", fmt.Errorf("incomplete virtiofs params in kernel cmdline (need both virtiofs.token and virtiofs.port)")
	}
	if p9Token != "" || p9Port != "" {
		return "", "", "", fmt.Errorf("incomplete 9p params in kernel cmdline (need both p9.token and p9.port)")
	}

	return "", "", "", fmt.Errorf("no filesystem sharing params found in kernel cmdline")
}
