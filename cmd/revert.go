package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var revertForce bool

var revertCmd = &cobra.Command{
	Use:     "revert <instance-name> <label>",
	Aliases: []string{"rollback"},
	GroupID: groupState,
	Short:   "Revert to a named snapshot (stops dockerd, rollbacks ZFS, restarts dockerd)",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := apiClient().RevertStream(cmd.Context(), args[0], args[1], revertForce, progressPrinter); err != nil {
			return fmt.Errorf("reverting instance: %w", err)
		}
		return nil
	},
}

func init() {
	revertCmd.Flags().BoolVarP(&revertForce, "force", "f", false, "destroy intermediate snapshots if necessary")
	rootCmd.AddCommand(revertCmd)
}
