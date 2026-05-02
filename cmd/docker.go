package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"
)

var dockerCmd = &cobra.Command{
	Use:     "docker <instance-name> -- <docker-args...>",
	GroupID: groupAccess,
	Short:   "Run docker commands against an instance's daemon",
	Long: `Passes through docker commands to the specified instance's Docker daemon.

Example:
  dockersnap docker env1 -- ps
  dockersnap docker env1 -- images
  dockersnap docker env1 -- exec -it mycontainer bash`,
	Args:               cobra.MinimumNArgs(1),
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		instanceName := args[0]
		var dockerArgs []string
		for i, arg := range args {
			if arg == "--" {
				dockerArgs = args[i+1:]
				break
			}
		}
		if len(dockerArgs) == 0 && len(args) > 1 {
			dockerArgs = args[1:]
		}

		// Get socket path from the API
		inst, err := apiClient().Get(cmd.Context(), instanceName)
		if err != nil {
			return fmt.Errorf("getting instance: %w", err)
		}

		dockerBin, err := exec.LookPath("docker")
		if err != nil {
			return fmt.Errorf("docker not found in PATH: %w", err)
		}

		fullArgs := append([]string{"docker", "-H", "unix://" + inst.Socket}, dockerArgs...)
		return syscall.Exec(dockerBin, fullArgs, os.Environ())
	},
}

func init() {
	rootCmd.AddCommand(dockerCmd)
}
