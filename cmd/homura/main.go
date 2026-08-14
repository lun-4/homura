package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/lun-4/homura/internal/commands"
	"github.com/spf13/cobra"
)

var (
	forceFlag        bool
	ninepTargetDir   string // Global flag for all 9p commands
	ninepTargetPID   int    // Target 9passthrough by PID
	ninepTargetVMID  int    // Target VM by slot number
	shareModeFlag    string // Filesystem sharing mode: "9p" or "virtiofs"
	vmForegroundFlag bool   // Run the VM chained to this terminal (--fg)
)

var rootCmd = &cobra.Command{
	Use:   "homura",
	Short: "Manage temporary git repository copies",
	Long:  `homura is a CLI tool for copying git repositories to temporary locations for isolated development work.`,
}

var cloneCmd = &cobra.Command{
	Use:   "clone <branch-name> [base]",
	Short: "Copy current repo to .homura/<branch-name>/",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		base := ""
		if len(args) > 1 {
			base = args[1]
		}
		return commands.Clone(args[0], base)
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
	Short: "Launch a QEMU microvm with filesystem sharing",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := ""
		if len(args) > 0 {
			branchName = args[0]
		}
		return commands.RunVM(cmd, args, branchName, shareModeFlag, vmForegroundFlag)
	},
}

var vmAttachCmd = &cobra.Command{
	Use:   "attach [branch]",
	Short: "Attach an interactive serial console to a running VM (Ctrl-] to detach)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := ""
		if len(args) > 0 {
			branchName = args[0]
		}
		return commands.VMAttach(branchName)
	},
}

var vmStopCmd = &cobra.Command{
	Use:   "stop [branch|vmid]",
	Short: "Stop a running VM and clean up its resources",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := ""
		if len(args) > 0 {
			branchName = args[0]
		}
		return commands.VMStop(branchName)
	},
}

var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Manage the homura VM daemon",
	Hidden: true,
}

var daemonRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the homura VM daemon in the foreground",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return commands.DaemonRun()
	},
}

var vmSshCmd = &cobra.Command{
	Use:   "ssh [branch]",
	Short: "SSH into the VM (uses branch arg, then default branch, then current directory)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branchName := ""
		if len(args) > 0 {
			branchName = args[0]
		}
		return commands.VMSsh(cmd, args, branchName)
	},
}

var vmLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all running VMs",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return commands.VMLs()
	},
}

var ninepCmd = &cobra.Command{
	Use:   "9p",
	Short: "Control 9p filesystem passthrough for running VMs",
	Long:  "Manage exposed paths and handle VM path requests for 9p filesystem sharing",
}

var ninepExposeCmd = &cobra.Command{
	Use:   "expose <path> [ro]",
	Short: "Expose a host path to the VM (add 'ro' for read-only)",
	Args:  cobra.RangeArgs(1, 2),
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
	Use:     "approve <request-id>",
	Aliases: []string{"ok"},
	Short:   "Approve a VM path request",
	Args:    cobra.ExactArgs(1),
	RunE:    commands.NinePReqApprove,
}

var ninepReqDenyCmd = &cobra.Command{
	Use:     "deny <request-id> [reason...]",
	Aliases: []string{"no"},
	Short:   "Deny a VM path request with optional reason",
	Args:    cobra.MinimumNArgs(1),
	RunE:    commands.NinePReqDeny,
}

func init() {
	rmCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Force removal even with uncommitted changes")

	// VM command flags
	vmCmd.Flags().StringVar(&shareModeFlag, "share-mode", "virtiofs",
		"Filesystem sharing mode: '9p' (default, uses FUSE) or 'virtiofs' (uses kernel driver)")
	vmCmd.Flags().BoolVar(&vmForegroundFlag, "fg", false,
		"Run the VM chained to this terminal instead of detached under the daemon")

	rootCmd.AddCommand(cloneCmd)
	rootCmd.AddCommand(shCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(lsCmd)
	rootCmd.AddCommand(vmCmd)
	rootCmd.AddCommand(ninepCmd)
	rootCmd.AddCommand(daemonCmd)

	// Add vm subcommands
	vmCmd.AddCommand(vmSshCmd)
	vmCmd.AddCommand(vmLsCmd)
	vmCmd.AddCommand(vmAttachCmd)
	vmCmd.AddCommand(vmStopCmd)

	// Add daemon subcommands
	daemonCmd.AddCommand(daemonRunCmd)

	// Add -d, -p, and -vm flags to all 9p commands
	ninepCmd.PersistentFlags().StringVarP(&ninepTargetDir, "dir", "d", "",
		"Target VM by working directory (defaults to current directory)")
	ninepCmd.PersistentFlags().IntVarP(&ninepTargetPID, "pid", "p", 0,
		"Target VM by 9passthrough PID")
	ninepCmd.PersistentFlags().IntVar(&ninepTargetVMID, "vm", 0,
		"Target VM by slot number")

	// Make flags available to commands package
	commands.NinePTargetDir = &ninepTargetDir
	commands.NinePTargetPID = &ninepTargetPID
	commands.NinePTargetVMID = &ninepTargetVMID

	ninepCmd.AddCommand(ninepExposeCmd)
	ninepCmd.AddCommand(ninepUnexposeCmd)
	ninepCmd.AddCommand(ninepListCmd)
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
