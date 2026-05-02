package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "ps"},
	GroupID: groupAdmin,
	Short:   "List all instances with status and disk usage",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		instances, err := apiClient().List(cmd.Context())
		if err != nil {
			return fmt.Errorf("listing instances: %w", err)
		}

		if len(instances) == 0 {
			fmt.Println("No instances found.")
			return nil
		}

		// Sort by creation date (oldest first)
		sort.Slice(instances, func(i, j int) bool {
			return instances[i].CreatedAt.Before(instances[j].CreatedAt)
		})

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tCREATED\tSUBNET\tSNAPSHOTS\tCLONE OF")
		for _, inst := range instances {
			created := inst.CreatedAt.Format("2006-01-02 15:04")
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
				inst.Name, inst.Status, created, inst.Subnet, len(inst.Snapshots), inst.CloneOf)
		}
		w.Flush()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
