package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// healthHandler runs cheap kubectl probes against the cluster from inside
// the instance's network namespace (kind's API server only listens on
// 127.0.0.1 inside the netns).
func healthHandler(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.HealthResponse, error) {
	clusterName := resolveClusterName(ctx, in)

	// Get a kubeconfig kubectl can use. We can't reuse the patched one from
	// access — that's for the API client. Use kind's raw kubeconfig and
	// run kubectl inside the netns to reach 127.0.0.1.
	rawKC, err := kindRawKubeconfig(ctx, in, clusterName)
	if err != nil {
		return &pluginsdk.HealthResponse{
			Healthy: false,
			Checks: []pluginsdk.HealthCheck{
				{Name: "kubeconfig", OK: false, Message: err.Error()},
			},
		}, nil
	}

	// Stash the kubeconfig in a temp file the netns can see (kubectl reads it
	// via path; the netns shares the host's mount namespace).
	tmpDir := filepath.Join("/tmp", "dockersnap-plugin-kind-"+in.InstanceName)
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return &pluginsdk.HealthResponse{
			Healthy: false,
			Checks: []pluginsdk.HealthCheck{
				{Name: "kubeconfig", OK: false, Message: err.Error()},
			},
		}, nil
	}
	kcPath := filepath.Join(tmpDir, "kubeconfig")
	if err := os.WriteFile(kcPath, []byte(rawKC), 0o600); err != nil {
		return &pluginsdk.HealthResponse{
			Healthy: false,
			Checks: []pluginsdk.HealthCheck{
				{Name: "kubeconfig", OK: false, Message: err.Error()},
			},
		}, nil
	}

	checks := []pluginsdk.HealthCheck{}

	// Probe 1: API server reachability via the /healthz endpoint.
	apiCheck := pluginsdk.HealthCheck{Name: "api-server"}
	if out, err := nsKubectl(ctx, in, kcPath,
		"--request-timeout=5s", "get", "--raw", "/healthz"); err != nil {
		apiCheck.OK = false
		apiCheck.Message = strings.TrimSpace(string(out)) + ": " + err.Error()
	} else {
		apiCheck.OK = true
		apiCheck.Message = strings.TrimSpace(string(out))
	}
	checks = append(checks, apiCheck)

	// Probe 2: nodes Ready.
	nodesCheck := pluginsdk.HealthCheck{Name: "nodes-ready"}
	if out, err := nsKubectl(ctx, in, kcPath,
		"--request-timeout=5s", "get", "nodes",
		"-o", `jsonpath={.items[*].status.conditions[?(@.type=="Ready")].status}`); err != nil {
		nodesCheck.OK = false
		nodesCheck.Message = err.Error()
	} else {
		ready := strings.Fields(strings.TrimSpace(string(out)))
		allTrue := len(ready) > 0
		for _, v := range ready {
			if v != "True" {
				allTrue = false
				break
			}
		}
		nodesCheck.OK = allTrue
		if allTrue {
			nodesCheck.Message = "all nodes Ready"
		} else {
			nodesCheck.Message = "some nodes not Ready: " + strings.TrimSpace(string(out))
		}
	}
	checks = append(checks, nodesCheck)

	healthy := true
	for _, c := range checks {
		if !c.OK {
			healthy = false
			break
		}
	}
	if !healthy {
		in.Logger.Warn("kind workload reports unhealthy",
			"cluster", clusterName, "checks", checks)
	} else {
		in.Logger.Debug("kind workload healthy", "cluster", clusterName)
	}
	return &pluginsdk.HealthResponse{Healthy: healthy, Checks: checks}, nil
}

func kindRawKubeconfig(ctx context.Context, in *pluginsdk.Context, clusterName string) (string, error) {
	cmd := exec.CommandContext(ctx, "kind", "get", "kubeconfig", "--name", clusterName)
	cmd.Env = kindEnv(in)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("kind get kubeconfig --name %s: %w (%s)",
			clusterName, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// nsKubectl runs kubectl inside the instance's network namespace using the
// given kubeconfig file. Kind's API server binds 127.0.0.1 inside the netns,
// so kubectl has to be in the same netns to reach it.
func nsKubectl(ctx context.Context, in *pluginsdk.Context, kcPath string, args ...string) ([]byte, error) {
	full := append([]string{"--kubeconfig", kcPath}, args...)
	cmd := pluginsdk.NsenterCommand(ctx, in.Instance.NetnsName, "kubectl", full...)
	return cmd.CombinedOutput()
}
