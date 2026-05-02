package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var workloadCmd = &cobra.Command{
	Use:     "workload",
	Aliases: []string{"wl"},
	GroupID: groupAccess,
	Short:   "Inspect an instance's workload (plugin describe / health)",
}

var workloadDescribeCmd = &cobra.Command{
	Use:   "describe <instance-name>",
	Short: "Print the workload's describe response (plugin metadata)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := apiClient().Workload(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("workload describe: %w", err)
		}
		return writeJSONIndent(os.Stdout, resp)
	},
}

var (
	workloadHealthFresh bool
)

var workloadHealthCmd = &cobra.Command{
	Use:   "health <instance-name>",
	Short: "Print the workload's cached health (or force a fresh check)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := apiClient().WorkloadHealth(cmd.Context(), args[0], workloadHealthFresh)
		if err != nil {
			return fmt.Errorf("workload health: %w", err)
		}
		return writeJSONIndent(os.Stdout, resp)
	},
}

func init() {
	workloadHealthCmd.Flags().BoolVar(&workloadHealthFresh, "fresh", false,
		"Force a synchronous health re-check on the daemon (bypass cache)")
	workloadCmd.AddCommand(workloadDescribeCmd)
	workloadCmd.AddCommand(workloadHealthCmd)
	rootCmd.AddCommand(workloadCmd)
}

// writeJSONIndent prints a value as 2-space-indented JSON for human consumption.
func writeJSONIndent(w *os.File, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
