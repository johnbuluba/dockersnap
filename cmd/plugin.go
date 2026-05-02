package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var pluginCmd = &cobra.Command{
	Use:     "plugin",
	Aliases: []string{"plugins"},
	GroupID: groupAdmin,
	Short:   "Inspect and manage workload plugins on the daemon",
}

var pluginListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List discovered plugins on the daemon",
	Aliases: []string{"ls"},
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		plugins, err := apiClient().Plugins(cmd.Context())
		if err != nil {
			return fmt.Errorf("plugin list: %w", err)
		}

		if len(plugins) == 0 {
			fmt.Println("No plugins found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATUS\tVERSION\tCONTRACT\tDESCRIPTION")
		for _, p := range plugins {
			contract := ""
			if len(p.SupportedContractVersions) > 0 {
				contract = p.SupportedContractVersions[0]
			}
			desc := p.Description
			if p.Status != "ready" && p.Error != "" {
				desc = p.Error
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				p.Name, p.Status, p.Version, contract, desc)
		}
		return w.Flush()
	},
}

var pluginDescribeCmd = &cobra.Command{
	Use:   "describe <plugin-name>",
	Short: "Show a plugin's full schema (config options, version, digests)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := apiClient().Plugin(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("plugin describe: %w", err)
		}
		return writeJSONIndent(os.Stdout, p)
	},
}

var pluginReloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Re-scan the plugin directory and re-run schema + init",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		plugins, err := apiClient().ReloadPlugins(cmd.Context())
		if err != nil {
			return fmt.Errorf("plugin reload: %w", err)
		}
		fmt.Printf("Reloaded %d plugin(s):\n", len(plugins))
		for _, p := range plugins {
			fmt.Printf("  %-15s  %s  %s\n", p.Name, p.Status, p.Version)
		}
		return nil
	},
}

func init() {
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginDescribeCmd)
	pluginCmd.AddCommand(pluginReloadCmd)
	rootCmd.AddCommand(pluginCmd)
}
