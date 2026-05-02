package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

var (
	accessFile   string
	accessFormat string
)

var accessCmd = &cobra.Command{
	Use:     "access <instance-name>",
	GroupID: groupAccess,
	Short:   "Show what the workload plugin's `access` returns (env vars, files, endpoints)",
	Long: `Calls /api/v1/instances/{name}/access on the daemon and prints the
plugin-supplied connection bundle.

Without flags, prints a human-readable summary on stdout. Useful for
inspecting what an instance exposes without materializing files (which is
` + "`dockersnap use`" + `'s job).

Common workflows:
  dockersnap access foo                       # summary
  dockersnap access foo --file kubeconfig     # print one file's content (e.g. for piping)
  dockersnap access foo -o json               # full structured response`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		c := apiClient()
		ctx := cmd.Context()

		// Refresh port forwardings so token-resolved URLs/ports are current.
		if _, err := c.RefreshPorts(ctx, name); err != nil {
			fmt.Fprintf(os.Stderr, "# Warning: refreshing ports: %v\n", err)
		}

		resp, err := c.Access(ctx, name)
		if err != nil {
			return fmt.Errorf("access: %w", err)
		}

		if accessFile != "" {
			for _, f := range resp.Files {
				if f.Name == accessFile {
					// Strip ${ACCESS_DIR} — the file is being printed to stdout,
					// there is no on-disk dir for it to refer to.
					content := strings.ReplaceAll(f.Content, pluginsdk.AccessDirToken, "")
					_, _ = os.Stdout.WriteString(content)
					if !strings.HasSuffix(content, "\n") {
						fmt.Println()
					}
					return nil
				}
			}
			names := make([]string, 0, len(resp.Files))
			for _, f := range resp.Files {
				names = append(names, f.Name)
			}
			return fmt.Errorf("file %q not in access response (have: %s)",
				accessFile, strings.Join(names, ", "))
		}

		switch accessFormat {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resp)
		case "", "text":
			printAccessSummary(name, resp)
			return nil
		default:
			return fmt.Errorf("unknown -o format %q (want: text, json)", accessFormat)
		}
	},
}

func printAccessSummary(name string, resp *pluginsdk.AccessResponse) {
	fmt.Printf("Instance: %s\n", name)
	if len(resp.Env) > 0 {
		fmt.Println("Env:")
		for k, v := range resp.Env {
			fmt.Printf("  %s=%s\n", k, v)
		}
	}
	if len(resp.Files) > 0 {
		fmt.Println("Files:")
		for _, f := range resp.Files {
			mode := f.Mode
			if mode == "" {
				mode = "0600"
			}
			fmt.Printf("  %-20s mode=%s  size=%d bytes\n", f.Name, mode, len(f.Content))
		}
		fmt.Println("  (use --file <name> to print a single file's content)")
	}
	if len(resp.Endpoints) > 0 {
		fmt.Println("Endpoints:")
		for _, ep := range resp.Endpoints {
			line := "  " + ep.Name
			if ep.URL != "" {
				line += " → " + ep.URL
			}
			if ep.Insecure {
				line += "  (insecure)"
			}
			if ep.Description != "" {
				line += "  # " + ep.Description
			}
			fmt.Println(line)
		}
	}
}

func init() {
	accessCmd.Flags().StringVar(&accessFile, "file", "",
		"Print the named file's content to stdout instead of a summary")
	accessCmd.Flags().StringVarP(&accessFormat, "output", "o", "",
		"Output format: text (default) or json")
	rootCmd.AddCommand(accessCmd)
}
