package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk/sdktest"
)

func TestAccess_NoForwardedPort(t *testing.T) {
	in := sdktest.NewContext(t).
		WithConfig(
			map[string]interface{}{"image": "x", "text": "y", "port": float64(5678)},
			[]pluginsdk.ConfigOption{
				{Name: "image", Type: pluginsdk.ConfigTypeString},
				{Name: "text", Type: pluginsdk.ConfigTypeString},
				{Name: "port", Type: pluginsdk.ConfigTypeInt},
			}).
		Build()

	resp, err := accessHandler(t.Context(), in)
	require.NoError(t, err)
	require.NotNil(t, resp)
	// DOCKER_HOST is daemon-injected (via injectDaemonEnv in
	// internal/api/server.go); the plugin response shouldn't carry it.
	assert.NotContains(t, resp.Env, "DOCKER_HOST",
		"plugin must not emit DOCKER_HOST; the daemon injects it")
	_, hasURL := resp.Env["ECHO_URL"]
	assert.False(t, hasURL, "ECHO_URL should be absent until the proxy surfaces the port")
	assert.Empty(t, resp.Endpoints)
}

func TestAccess_WithForwardedPort(t *testing.T) {
	in := sdktest.NewContext(t).
		WithConfig(
			map[string]interface{}{"image": "x", "text": "y", "port": float64(5678)},
			[]pluginsdk.ConfigOption{
				{Name: "image", Type: pluginsdk.ConfigTypeString},
				{Name: "text", Type: pluginsdk.ConfigTypeString},
				{Name: "port", Type: pluginsdk.ConfigTypeInt},
			}).
		WithForwardedPort("container:abc123/5678/tcp", 5678, 32999).
		Build()

	resp, err := accessHandler(t.Context(), in)
	require.NoError(t, err)
	require.Len(t, resp.Endpoints, 1)
	assert.Equal(t, "echo", resp.Endpoints[0].Name)
	assert.Equal(t, "container:abc123/5678/tcp", resp.Endpoints[0].HostPortLabel)
	assert.Contains(t, resp.Env["ECHO_URL"], pluginsdk.HostToken)
	assert.Contains(t, resp.Env["ECHO_URL"], "${PORT:container:abc123/5678/tcp}")
}

func TestAccess_PortMismatch(t *testing.T) {
	// Forwarded port doesn't match the configured one — endpoint absent.
	in := sdktest.NewContext(t).
		WithConfig(
			map[string]interface{}{"image": "x", "text": "y", "port": float64(5678)},
			[]pluginsdk.ConfigOption{
				{Name: "image", Type: pluginsdk.ConfigTypeString},
				{Name: "text", Type: pluginsdk.ConfigTypeString},
				{Name: "port", Type: pluginsdk.ConfigTypeInt},
			}).
		WithForwardedPort("container:abc/9999/tcp", 9999, 12345).
		Build()

	resp, err := accessHandler(t.Context(), in)
	require.NoError(t, err)
	assert.Empty(t, resp.Endpoints)
}
