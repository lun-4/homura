package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/hugelgupf/p9/p9"
)

const listenAddr = "0.0.0.0:0" // Use port 0 to auto-allocate a free port

var (
	authToken    string
	requestQueue *RequestQueue
)

// generateToken generates a secure random token
func generateToken() (string, error) {
	bytes := make([]byte, 32) // 256 bits
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// allocateControlPort finds an available port for the control server
func allocateControlPort() (int, error) {
	for port := 5641; port < 5651; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err == nil {
			listener.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available control ports (tried 5641-5650)")
}

func main() {
	// Parse optional initial paths from command line
	var initialPaths []string
	if len(os.Args) > 1 {
		initialPaths = os.Args[1:]
	}

	pid := os.Getpid()

	// Generate authentication token
	var err error
	authToken, err = generateToken()
	if err != nil {
		log.Fatalf("Failed to generate auth token: %v", err)
	}

	// Allocate control port
	controlPort, err := allocateControlPort()
	if err != nil {
		log.Fatalf("Failed to allocate control port: %v", err)
	}

	// Create PathRegistry and RequestQueue
	registry := NewPathRegistry()
	requestQueue = NewRequestQueue()

	// Add initial paths if provided (default to read-write)
	for _, path := range initialPaths {
		if err := registry.AddPath(path, false); err != nil {
			log.Printf("Warning: Failed to add initial path %s: %v", path, err)
		} else {
			log.Printf("Initially exposed: %s (rw)", path)
		}
	}

	// Create VirtualRoot with registry
	vroot := &VirtualRoot{Registry: registry}

	// Create 9p server
	server := p9.NewServer(vroot)

	// Create TCP listener for 9p
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()

	// Get the actual port that was allocated
	actualPort := listener.Addr().(*net.TCPAddr).Port

	// Start Unix socket control server
	unixSocketPath := fmt.Sprintf("/tmp/9p-control-%d.sock", pid)
	unixControlServer := NewUnixControlServer(unixSocketPath, registry, requestQueue, pid)
	if err := unixControlServer.Start(); err != nil {
		log.Fatalf("Failed to start Unix control server: %v", err)
	}
	defer unixControlServer.Stop()

	// Start TCP control server for VM access
	tcpControlServer := NewTCPControlServer(controlPort, authToken, registry, requestQueue, pid)
	if err := tcpControlServer.Start(); err != nil {
		log.Fatalf("Failed to start TCP control server: %v", err)
	}
	defer tcpControlServer.Stop()

	// Write token file for VM access (includes 9p port on third line)
	tokenFilePath := fmt.Sprintf("/tmp/9p-token-%d", pid)
	tokenContent := fmt.Sprintf("%s\n%d\n%d\n", authToken, controlPort, actualPort)
	if err := os.WriteFile(tokenFilePath, []byte(tokenContent), 0600); err != nil {
		log.Fatalf("Failed to write token file: %v", err)
	}
	defer os.Remove(tokenFilePath)

	log.Printf("9p server started with dynamic path management")
	log.Printf("9p listening on: 0.0.0.0:%d", actualPort)
	log.Printf("Unix control socket: %s", unixSocketPath)
	log.Printf("TCP control port: %d (token-protected)", controlPort)
	log.Printf("Token file: %s", tokenFilePath)
	log.Printf("Mount inside VM with: mount -t 9p -o trans=tcp,port=%d 10.0.2.2 /mnt", actualPort)
	log.Printf("Control with: 9pactl expose <path>")

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Goroutine to handle signals
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down...", sig)
		requestQueue.CloseAll()
		cancel()
		listener.Close()
	}()

	// Serve connections (automatically handles accept and per-connection goroutines)
	log.Println("Ready to accept connections...")
	if err := server.ServeContext(ctx, listener); err != nil {
		if ctx.Err() == nil {
			log.Fatalf("Server error: %v", err)
		}
	}
	log.Println("Server shutdown complete")
}
