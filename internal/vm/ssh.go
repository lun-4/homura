package vm

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// GenerateSSHKey generates an ephemeral ed25519 SSH key pair
// Returns paths to private and public key files
func GenerateSSHKey(stateDir string) (privPath, pubPath string, err error) {
	slog.Info("Generating ephemeral SSH key pair")

	// Generate ed25519 key pair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate key: %w", err)
	}

	// Convert to SSH format
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to convert public key: %w", err)
	}

	// Marshal private key to OpenSSH format
	privKeyBytes, err := ssh.MarshalPrivateKey(privKey, "")
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal private key: %w", err)
	}

	// Write private key
	privPath = filepath.Join(stateDir, "id_ed25519")
	if err := os.WriteFile(privPath, pem.EncodeToMemory(privKeyBytes), 0600); err != nil {
		return "", "", fmt.Errorf("failed to write private key: %w", err)
	}

	// Write public key
	pubPath = filepath.Join(stateDir, "id_ed25519.pub")
	pubKeyData := ssh.MarshalAuthorizedKey(sshPubKey)
	if err := os.WriteFile(pubPath, pubKeyData, 0644); err != nil {
		return "", "", fmt.Errorf("failed to write public key: %w", err)
	}

	slog.Info("SSH key pair generated", "private", privPath, "public", pubPath)
	return privPath, pubPath, nil
}

// FindFreePort finds an available TCP port on localhost
func FindFreePort() (int, error) {
	// Listen on port 0 to get a free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to find free port: %w", err)
	}
	defer listener.Close()

	// Get the port number
	addr := listener.Addr().(*net.TCPAddr)
	port := addr.Port

	slog.Debug("Found free port", "port", port)
	return port, nil
}

// GetCurrentUser returns the current username and home directory
func GetCurrentUser() (username, homeDir string, err error) {
	username = os.Getenv("USER")
	if username == "" {
		username = os.Getenv("LOGNAME")
	}
	if username == "" {
		return "", "", fmt.Errorf("could not determine username (USER or LOGNAME env vars not set)")
	}

	homeDir = os.Getenv("HOME")
	if homeDir == "" {
		return "", "", fmt.Errorf("could not determine home directory (HOME env var not set)")
	}

	slog.Debug("Current user detected", "username", username, "home", homeDir)
	return username, homeDir, nil
}
