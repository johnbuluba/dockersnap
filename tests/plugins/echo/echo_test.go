//go:build integration

package echo

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/client"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// TestEcho_PluginIsLoaded asserts the daemon discovered the echo plugin
// at startup. If this fails the rest of the suite is moot.
func TestEcho_PluginIsLoaded(t *testing.T) {
	t.Parallel()
	plugins, err := c.Plugins(context.Background())
	require.NoError(t, err, "listing plugins")

	var got *client.PluginInfo
	for i, p := range plugins {
		if p.Name == "echo" {
			got = &plugins[i]
			break
		}
	}
	require.NotNil(t, got, "echo plugin not discovered by daemon: %+v", plugins)
	assert.Equal(t, "ready", got.Status, "echo plugin not ready: %s", got.Error)
	assert.NotEmpty(t, got.Version, "plugin should advertise a version")
	assert.True(t, strings.HasPrefix(got.Version, "v"),
		"plugin version should be a SemVer-style string starting with 'v', got %q", got.Version)
}

// TestEcho_CreateAndAccess creates an instance backed by the echo plugin,
// reads its access bundle, curls the resulting URL, and verifies it
// returns the configured text.
func TestEcho_CreateAndAccess(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	name := instName("access")
	cleanup(t, name)
	defer cleanup(t, name)

	inst, err := c.Create(ctx, name, &client.WorkloadInline{Plugin: "echo"}, nil)
	require.NoError(t, err, "Create with --plugin echo")
	assert.Equal(t, "echo", inst.WorkloadPlugin)

	// Wait for the proxy's auto-discovery to surface the container's port
	// (the watcher polls every 5s by default, plus an event-driven path).
	access := waitForEchoEndpoint(t, ctx, name, 30*time.Second)
	require.NotEmpty(t, access.Endpoints, "no endpoints in access response")

	var echoEndpoint *struct {
		URL string
	}
	for _, ep := range access.Endpoints {
		if ep.Name == "echo" && ep.URL != "" {
			echoEndpoint = &struct{ URL string }{URL: ep.URL}
			break
		}
	}
	require.NotNil(t, echoEndpoint, "no 'echo' endpoint with resolved URL: %+v", access.Endpoints)
	t.Logf("echo URL: %s", echoEndpoint.URL)

	got := httpGet(t, echoEndpoint.URL, 10*time.Second)
	expected := "hello from " + name
	assert.Contains(t, got, expected,
		"echo response should contain configured text (default 'hello from %s')", name)

	// Env should include ECHO_URL.
	require.NotEmpty(t, access.Env["ECHO_URL"], "ECHO_URL should be present in env")
	assert.Equal(t, name, access.Env["DOCKERSNAP_INSTANCE"])
}

// TestEcho_Health checks the workload health endpoint reports healthy after
// the container is up.
func TestEcho_Health(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	name := instName("health")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, &client.WorkloadInline{Plugin: "echo"}, nil)
	require.NoError(t, err)

	// Force a fresh poll so we don't race the cache.
	deadline := time.Now().Add(60 * time.Second)
	var lastResp map[string]interface{}
	for time.Now().Before(deadline) {
		lastResp, err = c.WorkloadHealth(ctx, name, true)
		if err != nil {
			t.Logf("workload health: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if h, ok := lastResp["healthy"].(bool); ok && h {
			t.Logf("✓ workload healthy: %v", lastResp)
			return
		}
		t.Logf("not yet healthy: %v", lastResp)
		time.Sleep(2 * time.Second)
	}
	require.Failf(t, "workload never became healthy",
		"last response: %v", lastResp)
}

// TestEcho_Describe verifies the describe response carries the expected
// workload metadata.
func TestEcho_Describe(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	name := instName("describe")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, &client.WorkloadInline{Plugin: "echo"}, nil)
	require.NoError(t, err)

	desc, err := c.Workload(ctx, name)
	require.NoError(t, err)
	assert.Equal(t, "echo", desc["workload_type"])
	assert.Equal(t, "running", desc["status"],
		"echo container should be running; got %v", desc["status"])

	// Ports advertised in describe should contain the echo container port.
	ports, ok := desc["ports"].([]interface{})
	require.True(t, ok, "ports should be a list")
	require.NotEmpty(t, ports)
}

// TestEcho_InlineWorkload uses workload_inline to deploy with a custom
// echo text and verifies the response reflects it.
func TestEcho_InlineWorkload(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	name := instName("inline")
	cleanup(t, name)
	defer cleanup(t, name)

	custom := "custom-text-" + name
	_, err := c.Create(ctx, name, &client.WorkloadInline{
		Plugin: "echo",
		Config: map[string]interface{}{
			"text": custom,
			"port": 5678,
		},
	}, nil)
	require.NoError(t, err)

	access := waitForEchoEndpoint(t, ctx, name, 30*time.Second)
	require.NotEmpty(t, access.Endpoints)

	for _, ep := range access.Endpoints {
		if ep.Name != "echo" || ep.URL == "" {
			continue
		}
		got := httpGet(t, ep.URL, 10*time.Second)
		assert.Contains(t, got, custom, "inline-config text should be returned by the server")
		return
	}
	t.Fatalf("no echo endpoint with resolved URL")
}

// TestEcho_Teardown verifies that Delete tears down the echo container
// (the daemon invokes the plugin's teardown command before stopping
// dockerd).
func TestEcho_Teardown(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	name := instName("teardown")
	cleanup(t, name)
	// no defer — explicit Delete is the test

	_, err := c.Create(ctx, name, &client.WorkloadInline{Plugin: "echo"}, nil)
	require.NoError(t, err)

	require.NoError(t, c.Delete(ctx, name))

	// Instance should be gone.
	_, err = c.Get(ctx, name)
	assert.Error(t, err, "Get on a deleted instance must return an error")
}

// --- helpers ---

// waitForEchoEndpoint polls /access until the response includes an "echo"
// endpoint with a resolved URL (i.e. the proxy has surfaced the host port).
// On timeout the test fails.
func waitForEchoEndpoint(t *testing.T, ctx context.Context, name string, timeout time.Duration) *pluginsdk.AccessResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		access, err := c.Access(ctx, name)
		if err == nil {
			for _, ep := range access.Endpoints {
				if ep.Name == "echo" && ep.URL != "" {
					return access
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("timed out waiting for echo endpoint on instance %q", name)
	return nil
}

// httpGet does an HTTP GET with a per-request timeout and returns the body
// string. Failures fail the test.
func httpGet(t *testing.T, url string, timeout time.Duration) string {
	t.Helper()
	cli := &http.Client{Timeout: timeout}
	resp, err := cli.Get(url)
	require.NoError(t, err, "GET %s", url)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s status", url)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return strings.TrimSpace(string(body))
}
