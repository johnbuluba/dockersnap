package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:     "health",
	GroupID: groupAdmin,
	Short:   "Check daemon health and uptime",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := apiClient().Health(cmd.Context())
		if err != nil {
			return fmt.Errorf("health check failed: %w", err)
		}
		fmt.Printf("Status:     %s\n", status.Status)
		if status.Version != "" {
			fmt.Printf("Version:    %s\n", status.Version)
		}
		fmt.Printf("Uptime:     %s\n", status.Uptime)
		fmt.Printf("Started:    %s\n", status.StartedAt)
		fmt.Printf("Instances:  %d (%d running)\n", status.Instances, status.Running)
		if status.RunningHealthy+status.RunningUnhealthy > 0 {
			fmt.Printf("Workloads:  %d healthy, %d unhealthy\n",
				status.RunningHealthy, status.RunningUnhealthy)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(healthCmd)
}
