package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/johnbuluba/dockersnap/internal/client"
	"github.com/johnbuluba/dockersnap/pkg/version"
)

var (
	cfgFile string // bound by serveCmd only
	remote  string
	token   string
)

const defaultRemote = "http://127.0.0.1:9847"

// Command groups surfaced by `dockersnap --help`. Keep IDs short — cobra
// uses them as the group key and the title shows in help.
const (
	groupLifecycle = "lifecycle"
	groupState     = "state"
	groupAccess    = "access"
	groupAdmin     = "admin"
)

var rootCmd = &cobra.Command{
	Use:   "dockersnap",
	Short: "Instant snapshot, revert, and clone of Docker-based dev environments",
	Long: `dockersnap manages isolated Docker daemon instances on ZFS datasets,
providing instant snapshot/revert/clone of fully-deployed Docker-based environments.

Each instance is a self-contained environment with its own ZFS dataset,
Docker daemon, Docker network, and kind cluster.

All commands talk to the dockersnap daemon via its REST API; pass --remote
to point at a non-local one (or set DOCKERSNAP_REMOTE).

Tab completion is available for bash/zsh/fish/powershell — see
` + "`dockersnap completion --help`" + ` for installation instructions.`,
	// Don't dump the full usage block on every RunE error; the Error: line is enough.
	SilenceUsage: true,
	// Populated at link time via -ldflags. Adds --version automatically.
	Version: version.String(),
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&remote, "remote", "r", envOrDefault("DOCKERSNAP_REMOTE", defaultRemote), "API endpoint")
	rootCmd.PersistentFlags().StringVarP(&token, "token", "t", os.Getenv("DOCKERSNAP_TOKEN"), "API auth token (if server requires it)")

	// Order here is the order they render in --help.
	rootCmd.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle commands:"},
		&cobra.Group{ID: groupState, Title: "State commands:"},
		&cobra.Group{ID: groupAccess, Title: "Access commands:"},
		&cobra.Group{ID: groupAdmin, Title: "Admin commands:"},
	)

	// Surface a friendlier "Did you mean…?" — cobra's default suggestion
	// distance of 2 is fine; tightening the wording is what matters.
	rootCmd.SuggestionsMinimumDistance = 2
}

// apiClient returns the configured API client.
func apiClient() *client.Client {
	return client.New(remote, token)
}

// envOrDefault returns the env var value or a default.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func exitOnError(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
