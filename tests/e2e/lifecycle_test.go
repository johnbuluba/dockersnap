//go:build integration

package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/state"
)

func TestCreateAndValidateSystem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("create")
	cleanup(t, name)
	defer cleanup(t, name)

	inst, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")
	require.Equal(t, state.StatusRunning, inst.Status)

	dataset := cfg.DatasetPath(name)
	socket := cfg.SocketPath(name)
	mountpoint := cfg.MountPoint(name)
	nsName := "ds-" + name

	assert.True(t, zfsDatasetExists(t, dataset), "ZFS dataset %s should exist", dataset)
	assert.True(t, mountpointHasData(t, mountpoint), "mountpoint %s should have Docker data", mountpoint)
	assert.True(t, netnsExists(t, nsName), "network namespace %s should exist", nsName)
	assert.True(t, resolveConfExists(t, nsName), "/etc/netns/%s/resolv.conf should exist", nsName)

	unitName := fmt.Sprintf("dockersnap-%s.service", name)
	assert.True(t, systemdUnitActive(t, unitName), "systemd unit %s should be active", unitName)
	assert.True(t, socketExists(t, socket), "Docker socket %s should be reachable", socket)
	assert.True(t, dockerPing(t, socket), "Docker daemon should respond on %s", socket)

	comment := "dockersnap-" + name
	assert.True(t, iptablesHasComment(t, "nat", "POSTROUTING", comment),
		"iptables nat POSTROUTING rule with comment %q should exist", comment)
	assert.True(t, iptablesHasComment(t, "filter", "FORWARD", comment),
		"iptables filter FORWARD rule with comment %q should exist", comment)

	t.Logf("✓ Instance %s fully validated: dataset=%s subnet=%s", name, dataset, inst.Subnet)
}

func TestStopAndValidateCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("stop")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	socket := cfg.SocketPath(name)
	nsName := "ds-" + name
	unitName := fmt.Sprintf("dockersnap-%s.service", name)

	require.True(t, systemdUnitActive(t, unitName), "unit must be active before stop")

	require.NoError(t, c.Stop(ctx, name), "Stop")
	time.Sleep(1 * time.Second)

	assert.False(t, systemdUnitActive(t, unitName), "systemd unit should be inactive after stop")
	assert.False(t, socketExists(t, socket), "socket should not exist after stop")
	assert.False(t, netnsExists(t, nsName), "netns should not exist after stop")

	comment := "dockersnap-" + name
	assert.False(t, iptablesHasComment(t, "nat", "POSTROUTING", comment),
		"iptables nat rule should be removed after stop")
	assert.True(t, zfsDatasetExists(t, cfg.DatasetPath(name)),
		"ZFS dataset should survive stop (only delete destroys it)")

	t.Logf("✓ Stop cleanup validated for %s", name)
}

func TestDeleteAndValidateFullCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("delete")
	cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	dataset := cfg.DatasetPath(name)

	require.NoError(t, c.Delete(ctx, name), "Delete")
	time.Sleep(1 * time.Second)

	assert.False(t, zfsDatasetExists(t, dataset), "ZFS dataset should be destroyed")
	assert.False(t, netnsExists(t, "ds-"+name), "netns should be removed")
	assert.False(t, iptablesHasComment(t, "nat", "POSTROUTING", "dockersnap-"+name),
		"iptables rules should be removed")

	t.Logf("✓ Full cleanup validated for %s", name)
}

func TestStartRestoresState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("restart")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	socket := cfg.SocketPath(name)
	beforeCount := containerCount(t, socket)
	t.Logf("containers before stop: %d", beforeCount)

	require.NoError(t, c.Stop(ctx, name), "Stop")
	require.NoError(t, c.Start(ctx, name), "Start")

	assert.True(t, dockerPing(t, socket), "docker should respond after restart")
	assert.True(t, netnsExists(t, "ds-"+name), "netns should be recreated after start")
	assert.True(t, systemdUnitActive(t, fmt.Sprintf("dockersnap-%s.service", name)),
		"systemd unit should be active after start")

	t.Logf("✓ Start fully restores instance state")
}

func TestErrorCases(t *testing.T) {
	ctx := context.Background()

	t.Run("get nonexistent", func(t *testing.T) {
		_, err := c.Get(ctx, "nonexistent-xyz-"+prefix)
		assert.Error(t, err, "expected error for nonexistent instance")
	})

	t.Run("duplicate create", func(t *testing.T) {
		name := instName("dup")
		cleanup(t, name)
		defer cleanup(t, name)

		_, err := c.Create(ctx, name, nil, nil)
		require.NoError(t, err, "first create")

		_, err = c.Create(ctx, name, nil, nil)
		assert.Error(t, err, "expected error for duplicate create")
	})

	t.Run("revert nonexistent snapshot", func(t *testing.T) {
		name := instName("badsnap")
		cleanup(t, name)
		defer cleanup(t, name)

		_, err := c.Create(ctx, name, nil, nil)
		require.NoError(t, err)

		err = c.Revert(ctx, name, "nonexistent", false)
		assert.Error(t, err, "expected error for reverting to nonexistent snapshot")
	})

	t.Run("stop already stopped", func(t *testing.T) {
		name := instName("doubstop")
		cleanup(t, name)
		defer cleanup(t, name)

		_, err := c.Create(ctx, name, nil, nil)
		require.NoError(t, err)
		require.NoError(t, c.Stop(ctx, name), "first stop")

		err = c.Stop(ctx, name)
		assert.Error(t, err, "expected error for stopping already-stopped instance")
	})

	t.Run("invalid name rejected", func(t *testing.T) {
		_, err := c.Create(ctx, "../etc/passwd", nil, nil)
		assert.Error(t, err, "expected error for path-traversal name")
	})
}

func TestRevertDataIntegrity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("revert")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	dataset := cfg.DatasetPath(name)
	socket := cfg.SocketPath(name)
	markerPath := cfg.MountPoint(name) + "/.e2e-marker"

	require.NoError(t, os.WriteFile(markerPath, []byte("before-snapshot"), 0644))

	require.NoError(t, c.Snapshot(ctx, name, "golden", nil), "Snapshot golden")

	require.NoError(t, os.WriteFile(markerPath, []byte("after-snapshot"), 0644))

	require.NoError(t, c.Snapshot(ctx, name, "snap2", nil), "Snapshot snap2")
	require.True(t, zfsSnapshotExists(t, dataset, "snap2"), "snap2 should exist before revert")

	require.NoError(t, c.Revert(ctx, name, "golden", true), "Revert")

	data, err := os.ReadFile(markerPath)
	require.NoError(t, err, "reading marker after revert")
	assert.Equal(t, "before-snapshot", string(data), "marker content should be rolled back")

	assert.False(t, zfsSnapshotExists(t, dataset, "snap2"),
		"snap2 should have been destroyed by rollback -r")
	assert.True(t, zfsSnapshotExists(t, dataset, "golden"),
		"golden snapshot should still exist")
	assert.True(t, dockerPing(t, socket), "docker should respond after revert")

	t.Logf("✓ Revert validated: data rolled back, snap2 destroyed, golden preserved")
}
