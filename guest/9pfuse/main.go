package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
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
	pprofAddr := flag.String("pprof", "", "pprof HTTP address (e.g., :6060)")
	tracing := flag.Bool("trace", false, "enable operation tracing")
	flag.Parse()

	// Enable tracing if requested
	if *tracing {
		EnableTracing(true)
		log.Printf("Tracing enabled - send SIGUSR1 to dump stats, SIGUSR2 to reset")
	}

	// Start pprof server if requested
	if *pprofAddr != "" {
		go func() {
			log.Printf("Starting pprof server on %s", *pprofAddr)
			if err := http.ListenAndServe(*pprofAddr, nil); err != nil {
				log.Printf("pprof server error: %v", err)
			}
		}()
	}

	// Handle SIGUSR1 to dump trace stats
	usr1Chan := make(chan os.Signal, 1)
	signal.Notify(usr1Chan, syscall.SIGUSR1)
	go func() {
		for range usr1Chan {
			DumpStats()
		}
	}()

	// Handle SIGUSR2 to reset stats
	usr2Chan := make(chan os.Signal, 1)
	signal.Notify(usr2Chan, syscall.SIGUSR2)
	go func() {
		for range usr2Chan {
			ResetStats()
			log.Println("Trace stats reset")
		}
	}()

	// Setup logging to file
	logFile, err := os.OpenFile("/tmp/9pfuse.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

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
