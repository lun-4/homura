package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/lun-4/homura/internal/commands"
)

var (
	forceFlag   bool
	sandboxFlag bool
)

var rootCmd = &cobra.Command{
	Use:   "homura",
	Short: "Manage temporary git repository copies",
	Long:  `homura is a CLI tool for copying git repositories to temporary locations for isolated development work.`,
}

var cloneCmd = &cobra.Command{
	Use:   "clone <branch-name>",
	Short: "Copy current repo to .homura/<branch-name>/",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return commands.Clone(args[0])
	},
}

var shCmd = &cobra.Command{
	Use:   "sh [branch-name]",
	Short: "Open shell in copy",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := ""
		if len(args) > 0 {
			branchName = args[0]
		}
		return commands.Sh(branchName, sandboxFlag)
	},
}

var execCmd = &cobra.Command{
	Use:   "exec [branch-name] -- <command> [args...]",
	Short: "Execute a command in copy",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := ""
		var cmdArgs []string

		// Find the "--" separator
		sepIdx := -1
		for i, arg := range args {
			if arg == "--" {
				sepIdx = i
				break
			}
		}

		if sepIdx == -1 {
			// No separator: all args are the command (use default branch)
			cmdArgs = args
		} else if sepIdx == 0 {
			// Separator at start: use default branch, rest is command
			cmdArgs = args[1:]
		} else {
			// Separator after first arg: first arg is branch, rest is command
			branchName = args[0]
			cmdArgs = args[sepIdx+1:]
		}

		if len(cmdArgs) == 0 {
			return fmt.Errorf("no command provided")
		}

		return commands.Exec(branchName, sandboxFlag, cmdArgs)
	},
}

var rmCmd = &cobra.Command{
	Use:   "rm [branch-name]",
	Short: "Remove copy",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := ""
		if len(args) > 0 {
			branchName = args[0]
		}
		return commands.Rm(branchName, forceFlag)
	},
}

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all copies and show default branch",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return commands.Ls()
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Force removal even with uncommitted changes")
	shCmd.Flags().BoolVarP(&sandboxFlag, "sandbox", "s", false, "Run shell in FUSE sandbox with filesystem isolation")
	execCmd.Flags().BoolVarP(&sandboxFlag, "sandbox", "s", false, "Run command in FUSE sandbox with filesystem isolation")

	rootCmd.AddCommand(cloneCmd)
	rootCmd.AddCommand(shCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(lsCmd)
}

func main() {
	// Set up structured logging
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
