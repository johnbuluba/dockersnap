package instance

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

func TestHealthPoller_HealthyResultPopulatesCache(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.healthResp = &pluginsdk.HealthResponse{
		Healthy: true,
		Checks:  []pluginsdk.HealthCheck{{Name: "api", OK: true}},
	}

	ctx := context.Background()
	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)

	poller := NewHealthPoller(mgr, time.Hour, 3, slog.Default())
	poller.poll(ctx)

	got := poller.Cache().Get("env1")
	require.NotNil(t, got)
	assert.True(t, got.Healthy)
	assert.Equal(t, 0, got.ConsecutiveFails)
	require.NotNil(t, got.Response)
	assert.True(t, got.Response.Healthy)
}

func TestHealthPoller_UnhealthyBelowThresholdStaysHealthy(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.healthResp = &pluginsdk.HealthResponse{Healthy: false}

	ctx := context.Background()
	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)

	poller := NewHealthPoller(mgr, time.Hour, 3, slog.Default())

	poller.poll(ctx) // 1st failure
	got := poller.Cache().Get("env1")
	require.NotNil(t, got)
	assert.True(t, got.Healthy, "single failure should not flip cached Healthy when threshold is 3")
	assert.Equal(t, 1, got.ConsecutiveFails)

	poller.poll(ctx) // 2nd failure
	got = poller.Cache().Get("env1")
	assert.True(t, got.Healthy)
	assert.Equal(t, 2, got.ConsecutiveFails)

	poller.poll(ctx) // 3rd failure → flip
	got = poller.Cache().Get("env1")
	assert.False(t, got.Healthy, "after 3 consecutive failures Healthy should be false")
	assert.Equal(t, 3, got.ConsecutiveFails)
}

func TestHealthPoller_RecoveryResetsCounter(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.healthResp = &pluginsdk.HealthResponse{Healthy: false}

	ctx := context.Background()
	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)

	poller := NewHealthPoller(mgr, time.Hour, 3, slog.Default())
	poller.poll(ctx)
	poller.poll(ctx)

	pr.healthResp = &pluginsdk.HealthResponse{Healthy: true}
	poller.poll(ctx)

	got := poller.Cache().Get("env1")
	require.NotNil(t, got)
	assert.True(t, got.Healthy)
	assert.Equal(t, 0, got.ConsecutiveFails, "successful poll must reset failure counter")
}

func TestHealthPoller_NoWorkloadInstancesAreSkipped(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.healthResp = &pluginsdk.HealthResponse{Healthy: true}

	ctx := context.Background()
	_, err := mgr.Create(ctx, "plain", WorkloadOpts{}, nil)
	require.NoError(t, err)

	poller := NewHealthPoller(mgr, time.Hour, 3, slog.Default())
	poller.poll(ctx)

	assert.Nil(t, poller.Cache().Get("plain"),
		"instances without workload should not be polled or cached")
}

func TestHealthPoller_PluginErrorCountsAsFailure(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.healthErr = errors.New("plugin crashed")

	ctx := context.Background()
	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)

	poller := NewHealthPoller(mgr, time.Hour, 1, slog.Default())
	poller.poll(ctx)

	got := poller.Cache().Get("env1")
	require.NotNil(t, got)
	assert.Equal(t, 1, got.ConsecutiveFails)
	assert.False(t, got.Healthy, "single failure with threshold=1 should flip Healthy")
}

func TestHealthPoller_DeletedInstanceIsRemovedFromCache(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.healthResp = &pluginsdk.HealthResponse{Healthy: true}

	ctx := context.Background()
	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)

	poller := NewHealthPoller(mgr, time.Hour, 3, slog.Default())
	poller.poll(ctx)
	require.NotNil(t, poller.Cache().Get("env1"))

	require.NoError(t, mgr.Delete(ctx, "env1", nil))
	poller.poll(ctx)

	assert.Nil(t, poller.Cache().Get("env1"),
		"deleted instance should be removed from health cache")
}

func TestHealthCache_GetReturnsCopy(t *testing.T) {
	c := NewHealthCache()
	c.Set("env1", &HealthEntry{Healthy: true, ConsecutiveFails: 0})

	got := c.Get("env1")
	got.Healthy = false // mutate the copy

	again := c.Get("env1")
	assert.True(t, again.Healthy, "cache must return a copy, not a shared pointer")
}
