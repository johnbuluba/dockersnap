//go:build integration

package echo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/client"
)

// TestEcho_DeployFailureCleansUp confirms the rollback path in
// Manager.Create when the plugin's deploy fails. Uses workload_inline with
// a deliberately-invalid image so docker run fails — the daemon should run
// teardown, stop dockerd, destroy the dataset, and roll back state.
func TestEcho_DeployFailureCleansUp(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	name := instName("badimage")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, &client.WorkloadInline{
		Plugin: "echo",
		Config: map[string]interface{}{
			// Registry that won't resolve → docker pull errors → docker run errors.
			"image": "nonexistent.invalid.example/echo:does-not-exist",
			"text":  "ignored",
			"port":  5678,
		},
	}, nil)
	require.Error(t, err, "deploy with a bogus image must fail")

	// The instance must NOT remain in state — Create must have rolled back.
	insts, err := c.List(ctx)
	require.NoError(t, err)
	for _, inst := range insts {
		assert.NotEqual(t, name, inst.Name,
			"failed deploy must roll back state; %q should not be listed", name)
	}

	// Get must 404.
	_, err = c.Get(ctx, name)
	require.Error(t, err, "Get on a rolled-back instance must error")
}

// TestEcho_PortAutoDiscoveryAfterDeploy gives the proxy a moment to surface
// the published port — useful as a regression test for the auto-discovery
// watcher (event-driven primary + polling fallback).
func TestEcho_PortAutoDiscoveryAfterDeploy(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	name := instName("autoport")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, &client.WorkloadInline{Plugin: "echo"}, nil)
	require.NoError(t, err)

	// The watcher polls every 5s; events are usually instant. We give it
	// up to 30s just to be safe under proxy slowness.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ports, err := c.Ports(ctx, name)
		if err == nil && ports != nil && len(ports.Ports) > 0 {
			t.Logf("port surfaced after %s: %+v", time.Since(deadline.Add(-30*time.Second)),
				ports.Ports[0])
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("proxy never surfaced any port for echo container within 30s")
}
