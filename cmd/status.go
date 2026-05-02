package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:     "status <instance-name>",
	Aliases: []string{"info", "show"},
	GroupID: groupAdmin,
	Short:   "Show detailed status of an instance",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inst, err := apiClient().Get(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("getting instance status: %w", err)
		}

		fmt.Printf("Instance:    %s\n", inst.Name)
		fmt.Printf("Status:      %s\n", inst.Status)
		fmt.Printf("Dataset:     %s\n", inst.Dataset)
		fmt.Printf("Subnet:      %s\n", inst.Subnet)
		fmt.Printf("MetalLB IP:  %s\n", inst.MetalLBIP)
		fmt.Printf("Socket:      %s\n", inst.Socket)
		fmt.Printf("Created:     %s\n", inst.CreatedAt.Format("2006-01-02 15:04:05"))
		if inst.CloneOf != "" {
			fmt.Printf("Clone of:    %s\n", inst.CloneOf)
		}
		if len(inst.Snapshots) > 0 {
			fmt.Println("Snapshots:")
			for _, s := range inst.Snapshots {
				line := fmt.Sprintf("  - %s", s.Label)
				if !s.CreatedAt.IsZero() {
					line += fmt.Sprintf(" (%s)", s.CreatedAt.Format("2006-01-02 15:04"))
				}
				if len(s.Tags) > 0 {
					for k, v := range s.Tags {
						line += fmt.Sprintf(" [%s=%s]", k, v)
					}
				}
				fmt.Println(line)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
