package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/johnbuluba/dockersnap/internal/client"
	"github.com/johnbuluba/dockersnap/internal/instance"
)

var (
	createPlugin     string
	createConfigKVs  []string
	createConfigFile string
)

var createCmd = &cobra.Command{
	Use:     "create <instance-name>",
	Aliases: []string{"new"},
	GroupID: groupLifecycle,
	Short:   "Create a new instance (ZFS dataset + dockerd, optionally with a workload)",
	Long: `Create a new dockersnap instance.

A "workload" is a plugin-managed payload (e.g. a kind cluster) layered on top
of the isolated Docker environment. Two modes:

  - No workload (plain Docker):
      dockersnap create bench

  - With a plugin:
      dockersnap create foo --plugin kind --config retries=5 --config wait_ready=true
      dockersnap create foo --plugin kind --config-file ./kind-config.yaml

` + "`--config k=v`" + ` is repeatable; values are parsed as JSON literals when
they look like one (numbers, booleans, null, arrays, objects), otherwise
treated as strings. ` + "`--config-file`" + ` reads YAML/JSON; explicit
` + "`--config`" + ` flags overlay file values on conflict.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c := apiClient()
		name := args[0]

		inline, err := buildInlineWorkload()
		if err != nil {
			return err
		}

		inst, err := c.Create(cmd.Context(), name, inline,
			func(ev instance.ProgressEvent) {
				progressPrinter(ev)
			})
		if err != nil {
			return fmt.Errorf("creating instance: %w", err)
		}

		fmt.Printf("Instance %q created successfully.\n", inst.Name)
		fmt.Printf("  Dataset:  %s\n", inst.Dataset)
		fmt.Printf("  Subnet:   %s\n", inst.Subnet)
		fmt.Printf("  Socket:   %s\n", inst.Socket)
		if inst.WorkloadPlugin != "" {
			fmt.Printf("  Workload: %s\n", inst.WorkloadPlugin)
		}
		return nil
	},
}

// buildInlineWorkload returns nil when the user specified no inline flags;
// otherwise returns a fully-merged WorkloadInline ready for the API. Returns
// an error for malformed --config tokens, unreadable --config-file, or when
// --plugin is missing.
func buildInlineWorkload() (*client.WorkloadInline, error) {
	if createPlugin == "" && createConfigFile == "" && len(createConfigKVs) == 0 {
		return nil, nil
	}
	if createPlugin == "" {
		return nil, fmt.Errorf("--plugin is required when --config or --config-file is set")
	}

	cfg := map[string]interface{}{}

	if createConfigFile != "" {
		raw, err := os.ReadFile(createConfigFile)
		if err != nil {
			return nil, fmt.Errorf("reading --config-file: %w", err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("parsing --config-file: %w", err)
		}
	}

	for _, kv := range createConfigKVs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--config %q: expected key=value", kv)
		}
		cfg[k] = parseConfigValue(v)
	}

	return &client.WorkloadInline{Plugin: createPlugin, Config: cfg}, nil
}

// parseConfigValue tries to interpret v as a JSON literal so booleans and
// numbers don't have to be passed as strings; falls back to the raw string.
func parseConfigValue(v string) interface{} {
	// Special-case the obvious literals first — JSON unmarshal would also
	// handle these, but routing them explicitly keeps the bare-string fallback
	// path simple to reason about.
	switch v {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if _, err := strconv.ParseInt(v, 10, 64); err == nil {
		var n json.Number = json.Number(v)
		i, _ := n.Int64()
		return i
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	// Bracketed values look like JSON arrays/objects — try parsing.
	if (strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]")) ||
		(strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}")) {
		var any interface{}
		if err := json.Unmarshal([]byte(v), &any); err == nil {
			return any
		}
	}
	return v
}

func init() {
	createCmd.Flags().StringVar(&createPlugin, "plugin", "",
		"Plugin to deploy on top of the new instance")
	createCmd.Flags().StringArrayVar(&createConfigKVs, "config", nil,
		"Plugin config entry as key=value (repeatable). Values are JSON-parsed when possible.")
	createCmd.Flags().StringVar(&createConfigFile, "config-file", "",
		"Path to a YAML/JSON file with plugin config; merged with --config flags")
	rootCmd.AddCommand(createCmd)
}
