package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/johnbuluba/dockersnap/pkg/version"
)

var versionCmd = &cobra.Command{
	Use:     "version",
	GroupID: groupAdmin,
	Short:   "Print the dockersnap CLI and (when reachable) daemon versions",
	Long: `Print the local dockersnap binary's build version. When the daemon is
reachable, also prints the daemon's version — useful for spotting CLI/daemon
drift on remote setups (DOCKERSNAP_REMOTE).`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		local := version.String()
		fmt.Printf("CLI:    %s\n", local)

		// Best-effort daemon probe with a short timeout so `dockersnap version`
		// stays snappy when the daemon is unreachable.
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
		defer cancel()
		status, err := apiClient().Health(ctx)
		if err != nil {
			fmt.Printf("Daemon: unreachable at %s (%v)\n", remote, err)
			return
		}
		daemon := status.Version
		if daemon == "" {
			daemon = "(pre-version daemon)"
		}
		fmt.Printf("Daemon: %s (%s)\n", daemon, remote)
		if local != daemon && daemon != "(pre-version daemon)" {
			fmt.Printf("        ↑ CLI and daemon versions differ\n")
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
