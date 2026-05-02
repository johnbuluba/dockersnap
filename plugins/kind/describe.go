package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// describeHandler returns the workload's metadata + the resolved config.
func describeHandler(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.DescribeResponse, error) {
	clusterName := resolveClusterName(ctx, in)
	exists := kindClusterExists(ctx, in, clusterName)
	status := "ready"
	if !exists {
		status = "missing"
	}

	resp := &pluginsdk.DescribeResponse{
		WorkloadType: "kind",
		Status:       status,
		Ports: []pluginsdk.PortInfo{
			{Label: "kubernetes-api", ContainerPort: 6443, Protocol: "tcp"},
		},
		Config: map[string]interface{}{
			"cluster_name": clusterName,
		},
	}
	if v, ok := in.Config.OptString("kubernetes_version"); ok && v != "" {
		resp.Config["kubernetes_version"] = v
	}
	if v := in.Config.Path("kind_config"); v != "" {
		resp.Config["kind_config"] = v
	}

	if exists {
		// Best-effort node count.
		if n := countKindNodes(ctx, in, clusterName); n > 0 {
			resp.Details = map[string]interface{}{"node_count": n}
		}
	}
	return resp, nil
}

func kindClusterExists(ctx context.Context, in *pluginsdk.Context, name string) bool {
	cmd := exec.CommandContext(ctx, "kind", "get", "clusters")
	cmd.Env = kindEnv(in)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, c := range strings.Fields(strings.TrimSpace(string(out))) {
		if c == name {
			return true
		}
	}
	return false
}

func countKindNodes(ctx context.Context, in *pluginsdk.Context, clusterName string) int {
	cmd := exec.CommandContext(ctx, "kind", "get", "nodes", "--name", clusterName)
	cmd.Env = kindEnv(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	nodes := strings.Fields(strings.TrimSpace(string(out)))
	return len(nodes)
}
