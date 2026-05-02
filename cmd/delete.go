package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var deleteCmd = &cobra.Command{
	Use:     "delete <instance-name>",
	Short:   "Destroy an instance and all its snapshots",
	Aliases: []string{"rm", "destroy"},
	GroupID: groupLifecycle,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := apiClient().DeleteStream(cmd.Context(), args[0], progressPrinter); err != nil {
			return fmt.Errorf("deleting instance: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deleteCmd)
}
