package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// deployHandler runs `kind create cluster` against the instance's Docker
// daemon. The kind binary itself is idempotent-ish — it errors if the cluster
// already exists. Our retry logic handles flaky image pulls behind a
// corporate proxy.
func deployHandler(ctx context.Context, in *pluginsdk.Context, p *pluginsdk.Progress) error {
	clusterName := in.Config.String("cluster_name")
	kindConfig := in.Config.Path("kind_config")
	retries := in.Config.Int("retries")
	if retries < 1 {
		retries = 1
	}
	waitReady := in.Config.Bool("wait_ready")
	waitTimeout := "120s"
	if to, ok := in.Config.OptString("wait_timeout"); ok && to != "" {
		waitTimeout = to
	}

	p.Step("checking_prereqs", "Verifying kind binary and Docker socket")
	if _, err := exec.LookPath("kind"); err != nil {
		return p.Fail("checking_prereqs", err)
	}
	if _, err := os.Stat(in.Instance.Socket); err != nil {
		return p.Fail("checking_prereqs", fmt.Errorf("Docker socket %s missing: %w",
			in.Instance.Socket, err))
	}
	p.Done("checking_prereqs")

	p.Step("creating_cluster",
		fmt.Sprintf("Running kind create cluster --name %s", clusterName))
	in.Logger.Info("creating kind cluster",
		"cluster", clusterName,
		"kind_config", kindConfig,
		"wait_ready", waitReady,
		"retries", retries)
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		if attempt > 1 {
			p.Step("creating_cluster",
				fmt.Sprintf("Retry %d/%d after previous failure", attempt, retries))
			in.Logger.Warn("retrying kind create cluster",
				"cluster", clusterName,
				"attempt", attempt,
				"max_attempts", retries,
				"prev_error", lastErr.Error())
			// Best-effort cleanup from the previous failed attempt.
			cleanupCmd := exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", clusterName)
			cleanupCmd.Env = kindEnv(in)
			_, _ = cleanupCmd.CombinedOutput()
			time.Sleep(5 * time.Second)
		}

		args := []string{"create", "cluster", "--name", clusterName}
		if kindConfig != "" {
			args = append(args, "--config", kindConfig)
		}
		if waitReady {
			args = append(args, "--wait", waitTimeout)
		}
		if v, ok := in.Config.OptString("kubernetes_version"); ok && v != "" {
			args = append(args, "--image", "kindest/node:"+v)
		}

		cmd := exec.CommandContext(ctx, "kind", args...)
		cmd.Env = kindEnv(in)

		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		in.Logger.Debug("running kind", "args", args)
		if _, err := cmd.Output(); err != nil {
			lastErr = fmt.Errorf("kind create cluster: %w (%s)",
				err, strings.TrimSpace(stderr.String()))
			in.Logger.Warn("kind create cluster failed",
				"cluster", clusterName,
				"attempt", attempt,
				"error", err.Error(),
				"kind_stderr", strings.TrimSpace(stderr.String()))
			continue
		}

		// Success.
		p.Done("creating_cluster")
		in.Logger.Info("kind cluster created", "cluster", clusterName)
		p.Complete("kind cluster %q created", clusterName)
		return nil
	}

	in.Logger.Error("kind create cluster gave up after retries",
		"cluster", clusterName, "attempts", retries, "error", lastErr.Error())
	return p.Fail("creating_cluster", lastErr)
}

// kindEnv returns the environment variables to set when invoking kind: the
// instance's DOCKER_HOST plus the proxy variables the daemon passed through.
func kindEnv(in *pluginsdk.Context) []string {
	env := append(os.Environ(), pluginsdk.DockerHostEnv(in.Instance.Socket))
	for k, v := range in.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
