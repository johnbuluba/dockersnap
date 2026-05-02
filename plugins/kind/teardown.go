package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// teardownHandler runs `kind delete cluster`. Idempotent: succeeds even when
// the cluster is already gone (kind itself returns 0 in that case).
func teardownHandler(ctx context.Context, in *pluginsdk.Context, p *pluginsdk.Progress) error {
	clusterName := resolveClusterName(ctx, in)

	p.Step("deleting_cluster", fmt.Sprintf("Running kind delete cluster --name %s", clusterName))
	in.Logger.Info("deleting kind cluster", "cluster", clusterName)
	cmd := exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", clusterName)
	cmd.Env = kindEnv(in)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if _, err := cmd.Output(); err != nil {
		// kind delete prints "Cluster does not exist" on a missing cluster
		// and exits 0, so any non-zero is a real error.
		in.Logger.Error("kind delete cluster failed",
			"cluster", clusterName,
			"error", err.Error(),
			"kind_stderr", strings.TrimSpace(stderr.String()))
		return p.Fail("deleting_cluster",
			fmt.Errorf("kind delete cluster: %w (%s)", err, strings.TrimSpace(stderr.String())))
	}

	p.Done("deleting_cluster")
	p.Complete("kind cluster %q deleted", clusterName)
	return nil
}

// resolveClusterName tries the configured cluster_name first, falls back to
// listing clusters and picking the only one if there's exactly one. Used by
// teardown / access / describe / health for clones (where the kind cluster
// inherits the source's identity).
func resolveClusterName(ctx context.Context, in *pluginsdk.Context) string {
	configured := in.Config.String("cluster_name")
	if configured == "" {
		configured = in.InstanceName
	}

	// Trust the configured name first — kind get clusters is slow.
	checkCmd := exec.CommandContext(ctx, "kind", "get", "clusters")
	checkCmd.Env = kindEnv(in)
	out, err := checkCmd.Output()
	if err != nil {
		return configured
	}
	clusters := strings.Fields(strings.TrimSpace(string(out)))
	for _, c := range clusters {
		if c == configured {
			return configured
		}
	}
	if len(clusters) == 1 {
		return clusters[0]
	}
	return configured
}
