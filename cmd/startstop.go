package cmd

import (
	"fmt"

	"github.com/johnbuluba/dockersnap/internal/instance"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:     "start <instance-name>",
	GroupID: groupLifecycle,
	Short:   "Start the dockerd for a stopped instance",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := apiClient().StartStream(cmd.Context(), args[0], progressPrinter); err != nil {
			return fmt.Errorf("starting instance: %w", err)
		}
		return nil
	},
}

var stopCmd = &cobra.Command{
	Use:     "stop <instance-name>",
	GroupID: groupLifecycle,
	Short:   "Stop the dockerd for a running instance",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := apiClient().StopStream(cmd.Context(), args[0], progressPrinter); err != nil {
			return fmt.Errorf("stopping instance: %w", err)
		}
		return nil
	},
}

var restartCmd = &cobra.Command{
	Use:     "restart <instance-name>",
	GroupID: groupLifecycle,
	Short:   "Restart the dockerd for an instance (stop + start)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := apiClient()
		name := args[0]
		// Stop (ignore error if already stopped)
		_ = c.StopStream(cmd.Context(), name, progressPrinter)
		if err := c.StartStream(cmd.Context(), name, progressPrinter); err != nil {
			return fmt.Errorf("restarting instance: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
}

// progressPrinter prints progress events to stdout.
func progressPrinter(event instance.ProgressEvent) {
	switch event.Status {
	case "running":
		fmt.Printf("  → %s\n", event.Message)
	case "done":
		if event.Step == "complete" {
			fmt.Printf("  ✓ %s\n", event.Message)
		}
	case "error":
		fmt.Printf("  ✗ %s\n", event.Message)
	}
}
