package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/lun-4/homura/internal/commands"
)

var (
	forceFlag      bool
	ninepTargetDir string // Global flag for all 9p commands
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

var ninepCmd = &cobra.Command{
	Use:   "9p",
	Short: "Control 9p filesystem passthrough for running VMs",
	Long:  "Manage exposed paths and handle VM path requests for 9p filesystem sharing",
}

var ninepExposeCmd = &cobra.Command{
	Use:   "expose <path>",
	Short: "Expose a host path to the VM",
	Args:  cobra.ExactArgs(1),
	RunE:  commands.NinePExpose,
}

var ninepUnexposeCmd = &cobra.Command{
	Use:   "unexpose <path>",
	Short: "Remove a path from the exposed set",
	Args:  cobra.ExactArgs(1),
	RunE:  commands.NinePUnexpose,
}

var ninepListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all exposed paths",
	Args:  cobra.NoArgs,
	RunE:  commands.NinePList,
}

var ninepStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show 9p server status",
	Args:  cobra.NoArgs,
	RunE:  commands.NinePStatus,
}

var ninepReqCmd = &cobra.Command{
	Use:   "req",
	Short: "Manage VM path requests",
}

var ninepReqListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending path requests",
	Args:  cobra.NoArgs,
	RunE:  commands.NinePReqList,
}

var ninepReqApproveCmd = &cobra.Command{
	Use:   "approve <request-id>",
	Short: "Approve a VM path request",
	Args:  cobra.ExactArgs(1),
	RunE:  commands.NinePReqApprove,
}

var ninepReqDenyCmd = &cobra.Command{
	Use:   "deny <request-id> [reason]",
	Short: "Deny a VM path request",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  commands.NinePReqDeny,
}

func init() {
	rmCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Force removal even with uncommitted changes")

	rootCmd.AddCommand(cloneCmd)
	rootCmd.AddCommand(shCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(vmCmd)
	rootCmd.AddCommand(ninepCmd)

	// Add -d flag to all 9p commands
	ninepCmd.PersistentFlags().StringVarP(&ninepTargetDir, "dir", "d", "",
		"Target VM by working directory (defaults to current directory)")

	// Make flag available to commands package
	commands.NinePTargetDir = &ninepTargetDir

	ninepCmd.AddCommand(ninepExposeCmd)
	ninepCmd.AddCommand(ninepUnexposeCmd)
	ninepCmd.AddCommand(ninepListCmd)
	ninepCmd.AddCommand(ninepStatusCmd)
	ninepCmd.AddCommand(ninepReqCmd)
	ninepReqCmd.AddCommand(ninepReqListCmd)
	ninepReqCmd.AddCommand(ninepReqApproveCmd)
	ninepReqCmd.AddCommand(ninepReqDenyCmd)
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
