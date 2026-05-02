//go:build integration

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/client"
	"github.com/johnbuluba/dockersnap/internal/state"
)

// TestEdge_ReconcileAfterDaemonRestart proves the recovery path that
// reconcile sits on. Create an instance, kill the daemon, bring it back,
// verify the dockerd is still up (live-restore) and reconcile re-attached.
func TestEdge_ReconcileAfterDaemonRestart(t *testing.T) {
	ctx := context.Background()
	name := instName("reconcile")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err)

	unit := fmt.Sprintf("dockersnap-%s.service", name)
	require.True(t, systemdUnitActive(t, unit), "dockerd unit should be active before restart")

	t.Log("restarting dockersnap daemon")
	require.NoError(t, exec.Command("systemctl", "restart", "dockersnap").Run(),
		"restarting daemon")

	// Wait for the API to come back.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.List(ctx); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Reconcile should keep the instance reachable.
	inst, err := c.Get(ctx, name)
	require.NoError(t, err, "Get after daemon restart")
	assert.Equal(t, state.StatusRunning, inst.Status)
	assert.True(t, systemdUnitActive(t, unit),
		"dockerd unit must survive daemon restart (live-restore)")
	assert.True(t, dockerPing(t, cfg.SocketPath(name)),
		"docker socket must be reachable after daemon restart")
}

// TestEdge_MultipleSnapshotsRevertToMiddle verifies that reverting to a
// middle snapshot drops everything newer from both ZFS and the state file.
func TestEdge_MultipleSnapshotsRevertToMiddle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("revertmid")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err)

	for _, label := range []string{"a", "b", "c"} {
		require.NoError(t, c.Snapshot(ctx, name, label, nil), "snapshot %s", label)
	}

	dataset := cfg.DatasetPath(name)
	for _, label := range []string{"a", "b", "c"} {
		assert.True(t, zfsSnapshotExists(t, dataset, label), "snapshot %s should exist", label)
	}

	// Revert to the middle one. Force=true is required because newer snapshots
	// would otherwise block the rollback.
	require.NoError(t, c.Revert(ctx, name, "b", true), "revert to b")

	assert.True(t, zfsSnapshotExists(t, dataset, "a"), "a (older than target) survives")
	assert.True(t, zfsSnapshotExists(t, dataset, "b"), "b (target) survives")
	assert.False(t, zfsSnapshotExists(t, dataset, "c"), "c (newer than target) destroyed")

	// State should match.
	inst, err := c.Get(ctx, name)
	require.NoError(t, err)
	gotLabels := []string{}
	for _, s := range inst.Snapshots {
		gotLabels = append(gotLabels, s.Label)
	}
	assert.Equal(t, []string{"a", "b"}, gotLabels,
		"state.Snapshots should contain only a and b after revert")
}

// TestEdge_SubnetRecycling deletes instance 0 and re-creates a new instance,
// confirming the new one picks up the freed subnet index instead of growing
// the high-water mark.
func TestEdge_SubnetRecycling(t *testing.T) {
	ctx := context.Background()
	name1 := instName("recycle-1")
	name2 := instName("recycle-2")
	name3 := instName("recycle-3")
	cleanup(t, name1)
	cleanup(t, name2)
	cleanup(t, name3)
	defer cleanup(t, name1)
	defer cleanup(t, name2)
	defer cleanup(t, name3)

	a, err := c.Create(ctx, name1, nil, nil)
	require.NoError(t, err)
	b, err := c.Create(ctx, name2, nil, nil)
	require.NoError(t, err)

	// Delete the first one, then create a third — should get the freed
	// subnet index back.
	require.NoError(t, c.Delete(ctx, name1))
	cc, err := c.Create(ctx, name3, nil, nil)
	require.NoError(t, err)

	assert.NotEqual(t, a.Subnet, b.Subnet, "different instances must have different subnets")
	assert.Equal(t, a.Subnet, cc.Subnet,
		"freed subnet from %q (now deleted) should be reused for %q; got %s vs %s",
		name1, name3, a.Subnet, cc.Subnet)
}

// TestEdge_RestartCommand verifies that restart works on running and
// stopped instances and leaves the instance running afterwards.
func TestEdge_RestartCommand(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("running instance", func(t *testing.T) {
		name := instName("restart-running")
		cleanup(t, name)
		defer cleanup(t, name)

		_, err := c.Create(ctx, name, nil, nil)
		require.NoError(t, err)

		// Restart = stop + start via the API.
		require.NoError(t, c.Stop(ctx, name))
		require.NoError(t, c.Start(ctx, name))

		assert.True(t, dockerPing(t, cfg.SocketPath(name)),
			"docker should respond after restart")
	})

	t.Run("already stopped", func(t *testing.T) {
		name := instName("restart-stopped")
		cleanup(t, name)
		defer cleanup(t, name)

		_, err := c.Create(ctx, name, nil, nil)
		require.NoError(t, err)
		require.NoError(t, c.Stop(ctx, name))

		// Stopping again returns an error (already stopped); start should
		// succeed and bring dockerd back.
		require.NoError(t, c.Start(ctx, name))
		assert.True(t, dockerPing(t, cfg.SocketPath(name)))
	})
}

// TestEdge_LongInstanceName exercises the 32-character regex boundary plus
// the veth-name hashing path (>12 chars) and verifies the netns / iface
// still wire up.
func TestEdge_LongInstanceName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Our prefix already burns ~10 chars; pick a suffix that lands us at
	// exactly 32 characters total (the regex max).
	suffix := strings.Repeat("a", 32-len(prefix)-1)
	name := prefix + "-" + suffix
	require.LessOrEqual(t, len(name), 32, "test setup: name must fit the 32-char regex limit")
	cleanup(t, name)
	defer cleanup(t, name)

	inst, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "long-but-legal name should be accepted")
	assert.Equal(t, state.StatusRunning, inst.Status)

	nsName := "ds-" + name
	assert.True(t, netnsExists(t, nsName),
		"netns ds-<long-name> should be created (longer than IFNAMSIZ but the netns name is fine)")
	assert.True(t, dockerPing(t, cfg.SocketPath(name)),
		"docker socket should be reachable for a long-name instance — proves the hashed veth name still works")
}

// TestEdge_SnapshotWithTags verifies that --tag key=value is persisted
// through the API and visible in the state file.
func TestEdge_SnapshotWithTags(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("tags")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err)

	tags := map[string]string{
		"version": "2.5.0",
		"env":     "prod",
	}
	require.NoError(t, c.Snapshot(ctx, name, "tagged", tags))

	inst, err := c.Get(ctx, name)
	require.NoError(t, err)
	require.Len(t, inst.Snapshots, 1)
	assert.Equal(t, "tagged", inst.Snapshots[0].Label)
	assert.Equal(t, tags, inst.Snapshots[0].Tags,
		"snapshot tags should round-trip through the API")
	assert.False(t, inst.Snapshots[0].CreatedAt.IsZero(),
		"snapshot CreatedAt should be set")
}

// TestEdge_ReCreateAfterDelete confirms a name freed by Delete can be
// reused — guards against state-cleanup ordering bugs.
func TestEdge_ReCreateAfterDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("recreate")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err)
	require.NoError(t, c.Delete(ctx, name))

	// Same name, fresh instance — should land cleanly.
	inst, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create with a previously-deleted name must succeed")
	assert.Equal(t, state.StatusRunning, inst.Status)
	assert.True(t, dockerPing(t, cfg.SocketPath(name)))
}

// TestEdge_UnknownPlugin verifies the daemon rejects --plugin <name> for a
// plugin that's not loaded BEFORE doing any I/O — no half-created instance,
// no leaked dataset.
func TestEdge_UnknownPlugin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("badplugin")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, &client.WorkloadInline{
		Plugin: "this-plugin-does-not-exist",
	}, nil)
	require.Error(t, err, "unknown plugin must be rejected")

	// No instance should have been created.
	insts, err := c.List(ctx)
	require.NoError(t, err)
	for _, inst := range insts {
		assert.NotEqual(t, name, inst.Name,
			"failed Create must not leave a partial instance behind")
	}
}
