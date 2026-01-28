package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

const (
	defaultServerAddr = "10.0.2.2:5640"
	defaultMountpoint = "/mnt/host"
)

func main() {
	// Parse flags
	serverAddr := flag.String("server", defaultServerAddr, "9p server address")
	mountpoint := flag.String("mount", defaultMountpoint, "FUSE mount point")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Setup logging
	if !*debug {
		log.SetFlags(0)
	}

	log.Printf("Starting 9pfuse...")
	log.Printf("Connecting to 9p server at %s", *serverAddr)

	// Create 9p client
	client, err := NewP9Client(*serverAddr)
	if err != nil {
		log.Fatalf("Failed to create 9p client: %v", err)
	}
	defer client.Close()

	log.Printf("Connected to 9p server")

	// Create mountpoint if it doesn't exist
	if err := os.MkdirAll(*mountpoint, 0755); err != nil {
		log.Fatalf("Failed to create mountpoint: %v", err)
	}

	// Mount FUSE filesystem
	server, err := MountFUSE(*mountpoint, client)
	if err != nil {
		log.Fatalf("Failed to mount FUSE: %v", err)
	}
	defer server.Unmount()

	log.Printf("Mounted FUSE filesystem at %s", *mountpoint)
	log.Printf("Ready to serve requests")

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, unmounting...", sig)
		server.Unmount()
		os.Exit(0)
	}()

	// Wait for server to finish (blocks until unmount)
	server.Wait()

	fmt.Println("9pfuse exited cleanly")
}
