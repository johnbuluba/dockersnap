package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// accessHandler runs `kind get kubeconfig`, patches the server URL to use
// dockersnap's token placeholders, strips certificate-authority-data, and
// returns the result as a File entry. The daemon resolves ${HOST} and
// ${PORT:kubernetes-api}; the CLI resolves ${ACCESS_DIR}.
func accessHandler(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.AccessResponse, error) {
	clusterName := resolveClusterName(ctx, in)

	cmd := exec.CommandContext(ctx, "kind", "get", "kubeconfig", "--name", clusterName)
	cmd.Env = kindEnv(in)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kind get kubeconfig: %w (%s)",
			err, strings.TrimSpace(stderr.String()))
	}

	patched := patchKubeconfig(string(out))

	return &pluginsdk.AccessResponse{
		// DOCKER_HOST and DOCKERSNAP_INSTANCE are injected by the daemon
		// regardless of what the plugin returns — they're derivable from
		// instance state. Plugins should only emit env vars that are
		// genuinely plugin-specific (KUBECONFIG, ECHO_URL, etc.).
		Env: map[string]string{
			"KUBECONFIG": pluginsdk.AccessDirToken + "/kubeconfig",
		},
		Files: []pluginsdk.File{
			pluginsdk.FileFromString("kubeconfig", patched, 0o600),
		},
		Endpoints: []pluginsdk.Endpoint{
			{
				Name:          "kubernetes-api",
				Scheme:        "https",
				HostPortLabel: "kubernetes-api",
				Insecure:      true,
				Description:   "kubectl-compatible Kubernetes API",
			},
		},
	}, nil
}

// patchKubeconfig rewrites kind's emitted kubeconfig so it's reachable from
// outside the namespace and doesn't need the kind CA bundle.
//
//   - Replace `https://127.0.0.1:<port>` with `https://${HOST}:${PORT:kubernetes-api}`
//   - Strip `certificate-authority-data:` (the kind cert is for 127.0.0.1
//     only — once we change the host, it won't validate anyway)
//   - Add `insecure-skip-tls-verify: true` in its place, preserving indentation
func patchKubeconfig(in string) string {
	// Replace the server line. kind always emits exactly one
	// `https://127.0.0.1:<port>` so a single string replace is correct.
	if i := strings.Index(in, "https://127.0.0.1:"); i >= 0 {
		end := strings.IndexByte(in[i:], '\n')
		if end < 0 {
			end = len(in) - i
		}
		newServer := "https://" + pluginsdk.HostToken + ":" + pluginsdk.PortToken("kubernetes-api")
		in = in[:i] + newServer + in[i+end:]
	}

	// Replace certificate-authority-data with insecure-skip-tls-verify, preserving indent.
	lines := strings.Split(in, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "certificate-authority-data:") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
			lines[i] = indent + "insecure-skip-tls-verify: true"
		}
	}
	return strings.Join(lines, "\n")
}
