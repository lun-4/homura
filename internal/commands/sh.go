package commands

import (
	"fmt"
	"os"
)

// Sh opens a shell in the specified branch copy
func Sh(branchName string, sandboxed bool) error {
	// Get shell from environment or use /bin/sh
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	fmt.Printf("Opening shell in .homura/%s/\n", branchName)
	if branchName == "" {
		fmt.Println("(using default branch)")
	}
	fmt.Printf("Type 'exit' to return to original repository\n")

	return Exec(branchName, sandboxed, []string{shell})
}
