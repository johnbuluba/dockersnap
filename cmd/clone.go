package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var cloneCmd = &cobra.Command{
	Use:     "clone <instance-name> <snapshot-label> <new-instance-name>",
	GroupID: groupState,
	Short:   "Create a new instance from a snapshot (ZFS clone, new network, new dockerd)",
	Args:    cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := apiClient().CloneStream(cmd.Context(), args[0], args[1], args[2], progressPrinter); err != nil {
			return fmt.Errorf("cloning instance: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cloneCmd)
}
