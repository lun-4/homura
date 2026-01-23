package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/lun-4/homura/internal/commands"
)

var (
	forceFlag bool
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
		return commands.Sh(branchName)
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

var vmCmd = &cobra.Command{
	Use:   "vm [branch-name]",
	Short: "Launch a QEMU microvm with 9p mounts",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := ""
		if len(args) > 0 {
			branchName = args[0]
		}
		return commands.RunVM(cmd, args, branchName)
	},
}

func init() {
	rmCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Force removal even with uncommitted changes")

	rootCmd.AddCommand(cloneCmd)
	rootCmd.AddCommand(shCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(vmCmd)
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
