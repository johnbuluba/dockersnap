package cmd

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnbuluba/dockersnap/internal/state"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

var useUnset bool

var useCmd = &cobra.Command{
	Use:     "use <instance-name>",
	GroupID: groupAccess,
	Short:   "Configure shell environment for an instance",
	Long: `Configures shell environment variables to interact with an instance.
Designed to be used with eval:

  eval $(dockersnap use env1)

Behind the scenes, this calls /api/v1/instances/{name}/access on the daemon
and lets the bound workload plugin (if any) decide what to surface — kubeconfig
files, env vars, structured endpoint info. Plain-Docker instances get just
DOCKER_HOST.

To unset:
  eval $(dockersnap use --unset)

Shell prompt integration (add to ~/.bashrc or ~/.zshrc):
  if [ -n "$DOCKERSNAP_INSTANCE" ]; then
    PS1="[ds:$DOCKERSNAP_INSTANCE] $PS1"
  fi`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if useUnset {
			printUnset()
			return nil
		}
		if len(args) == 0 {
			return fmt.Errorf("instance name required (or use --unset)")
		}

		name := args[0]
		c := apiClient()
		ctx := cmd.Context()

		// Verify instance is running.
		inst, err := c.Get(ctx, name)
		if err != nil {
			return fmt.Errorf("getting instance: %w", err)
		}
		if inst.Status != state.StatusRunning {
			return fmt.Errorf("instance %q is %s (must be running)", name, inst.Status)
		}

		// Refresh port forwarding so the proxy state is current before access
		// is computed (kubeconfig URLs need the host_port).
		if _, err := c.RefreshPorts(ctx, name); err != nil {
			fmt.Fprintf(os.Stderr, "# Warning: refreshing ports: %v\n", err)
		}

		access, err := c.Access(ctx, name)
		if err != nil {
			return fmt.Errorf("getting access bundle: %w", err)
		}

		accessDir, err := materializeAccess(name, access)
		if err != nil {
			return fmt.Errorf("writing access files: %w", err)
		}

		printAccess(name, access, accessDir)
		return nil
	},
}

// materializeAccess writes every File entry to ~/.dockersnap/<name>/<file.Name>
// with the requested mode, then returns the access dir for env-var expansion.
// ${ACCESS_DIR} substitution happens here (the daemon left it as a literal).
func materializeAccess(name string, access *pluginsdk.AccessResponse) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".dockersnap", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	for _, f := range access.Files {
		mode, err := parseMode(f.Mode)
		if err != nil {
			return "", fmt.Errorf("file %q: %w", f.Name, err)
		}
		path := filepath.Join(dir, f.Name)
		content := strings.ReplaceAll(f.Content, pluginsdk.AccessDirToken, dir)
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			return "", fmt.Errorf("writing %s: %w", path, err)
		}
	}

	return dir, nil
}

func parseMode(s string) (os.FileMode, error) {
	if s == "" {
		return 0o600, nil
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q: %w", s, err)
	}
	return os.FileMode(v), nil
}

// printAccess prints the shell exports for stdout (consumable by eval) and a
// human-readable summary on stderr.
func printAccess(name string, access *pluginsdk.AccessResponse, accessDir string) {
	shell := detectShell()
	export := func(key, value string) {
		if shell == "fish" {
			fmt.Printf("set -gx %s %q;\n", key, value)
		} else {
			fmt.Printf("export %s=%q\n", key, value)
		}
	}

	// DOCKER_HOST and DOCKERSNAP_INSTANCE arrive in access.Env via the
	// daemon's injectDaemonEnv — no separate explicit export here.
	for k, v := range access.Env {
		v = strings.ReplaceAll(v, pluginsdk.AccessDirToken, accessDir)
		export(k, v)
	}

	fmt.Fprintf(os.Stderr, "# Configured for instance %q\n", name)
	if accessDir != "" && len(access.Files) > 0 {
		fmt.Fprintf(os.Stderr, "# Files:\n")
		for _, f := range access.Files {
			fmt.Fprintf(os.Stderr, "#   %s\n", filepath.Join(accessDir, f.Name))
		}
	}
	if len(access.Endpoints) > 0 {
		fmt.Fprintf(os.Stderr, "# Endpoints:\n")
		for _, ep := range access.Endpoints {
			line := fmt.Sprintf("#   %s", ep.Name)
			if ep.URL != "" {
				line += " → " + ep.URL
			}
			if ep.Insecure {
				line += " (insecure)"
			}
			fmt.Fprintln(os.Stderr, line)
		}
	}
}

func extractHost(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		if h := u.Hostname(); h != "" {
			return h
		}
	}
	host := rawURL
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	if idx := strings.IndexByte(host, '/'); idx >= 0 {
		host = host[:idx]
	}
	if idx := strings.LastIndexByte(host, ':'); idx >= 0 {
		host = host[:idx]
	}
	return host
}

func printUnset() {
	shell := detectShell()
	switch shell {
	case "fish":
		fmt.Println("set -e KUBECONFIG;")
		fmt.Println("set -e DOCKERSNAP_INSTANCE;")
		fmt.Println("set -e DOCKER_HOST;")
	default:
		fmt.Println("unset KUBECONFIG")
		fmt.Println("unset DOCKERSNAP_INSTANCE")
		fmt.Println("unset DOCKER_HOST")
	}
}

func detectShell() string {
	if os.Getenv("FISH_VERSION") != "" {
		return "fish"
	}
	if strings.Contains(os.Getenv("SHELL"), "fish") {
		return "fish"
	}
	return "posix"
}

func init() {
	useCmd.Flags().BoolVarP(&useUnset, "unset", "u", false, "Unset environment variables")
	rootCmd.AddCommand(useCmd)
}
