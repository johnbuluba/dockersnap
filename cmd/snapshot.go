package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var snapshotTags []string

var snapshotCmd = &cobra.Command{
	Use:     "snapshot <instance-name> <label>",
	Aliases: []string{"snap"},
	GroupID: groupState,
	Short:   "Capture a named snapshot (stops dockerd, snapshots ZFS, restarts dockerd)",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		tags := parseTags(snapshotTags)
		if err := apiClient().SnapshotStream(cmd.Context(), args[0], args[1], tags, progressPrinter); err != nil {
			return fmt.Errorf("creating snapshot: %w", err)
		}
		return nil
	},
}

func init() {
	// No `-t` shorthand: the root command's --token already owns it as a
	// persistent flag, and cobra panics if a subcommand tries to redefine it.
	snapshotCmd.Flags().StringArrayVar(&snapshotTags, "tag", nil, "Tag in key=value format (can be repeated)")
	rootCmd.AddCommand(snapshotCmd)
}

// parseTags converts ["key=value", ...] to map[string]string.
func parseTags(raw []string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	tags := make(map[string]string, len(raw))
	for _, t := range raw {
		k, v, _ := strings.Cut(t, "=")
		tags[k] = v
	}
	return tags
}
