package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	Path  string `json:"path"`
	Token string `json:"token"`
}

// RequestResult represents the result of a successful request
type RequestResult struct {
	Approved bool   `json:"approved"`
	Path     string `json:"path"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <path>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nRequest a path to be exposed by the host's 9passthrough server.\n")
		fmt.Fprintf(os.Stderr, "This command blocks until the request is approved or denied.\n")
		os.Exit(1)
	}

	// Get path argument
	path := os.Args[1]

	// Convert to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get absolute path: %v\n", err)
		os.Exit(1)
	}

	// Read kernel cmdline to get token and port
	token, port, err := readKernelParams()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nThis command must be run inside a VM with 9passthrough configured.\n")
		os.Exit(1)
	}

	// Connect to control server
	hostAddr := fmt.Sprintf("10.0.2.2:%s", port)
	conn, err := net.Dial("tcp", hostAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect to host control server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Build request
	params := RequestParams{
		Path:  absPath,
		Token: token,
	}

	request := RPCRequest{
		Method: "request",
		Params: params,
		ID:     1,
	}

	// Send request
	data, err := json.Marshal(request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to marshal request: %v\n", err)
		os.Exit(1)
	}

	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to send request: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Requesting path: %s\n", absPath)
	fmt.Println("Waiting for host approval...")

	// Read response (this blocks until approved/denied)
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to read response: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Error: connection closed by host\n")
		}
		os.Exit(1)
	}

	// Parse response
	var response RPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse response: %v\n", err)
		os.Exit(1)
	}

	// Check for error
	if response.Error != nil {
		fmt.Fprintf(os.Stderr, "Request denied: %s\n", response.Error.Message)
		os.Exit(1)
	}

	// Parse result
	resultData, err := json.Marshal(response.Result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to process result: %v\n", err)
		os.Exit(1)
	}

	var result RequestResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to parse result: %v\n", err)
		os.Exit(1)
	}

	if result.Approved {
		fmt.Printf("✓ Request approved! Path exposed: %s\n", result.Path)
		os.Exit(0)
	}

	fmt.Fprintf(os.Stderr, "Request denied\n")
	os.Exit(1)
}

// readKernelParams reads p9.token and p9.port from /proc/cmdline
func readKernelParams() (token, port string, err error) {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return "", "", fmt.Errorf("failed to read /proc/cmdline: %w", err)
	}

	cmdline := string(data)
	parts := strings.Fields(cmdline)

	for _, part := range parts {
		if strings.HasPrefix(part, "p9.token=") {
			token = strings.TrimPrefix(part, "p9.token=")
		} else if strings.HasPrefix(part, "p9.port=") {
			port = strings.TrimPrefix(part, "p9.port=")
		}
	}

	if token == "" {
		return "", "", fmt.Errorf("p9.token not found in kernel cmdline")
	}

	if port == "" {
		return "", "", fmt.Errorf("p9.port not found in kernel cmdline")
	}

	return token, port, nil
}
