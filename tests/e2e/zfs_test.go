//go:build integration

package e2e

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/state"
)

func TestSnapshotCreatesZFSSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("snap")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	dataset := cfg.DatasetPath(name)

	require.NoError(t, c.Snapshot(ctx, name, "golden", nil), "Snapshot")

	assert.True(t, zfsSnapshotExists(t, dataset, "golden"),
		"ZFS snapshot %s@golden should exist", dataset)

	// Instance should be running (dockerd restarted after snapshot)
	inst, err := c.Get(ctx, name)
	require.NoError(t, err, "Get")
	assert.Equal(t, state.StatusRunning, inst.Status, "expected running after snapshot")
	assert.True(t, dockerPing(t, cfg.SocketPath(name)), "docker should respond after snapshot")

	t.Logf("✓ Snapshot validated: %s@golden", dataset)
}

func TestZFSProperties(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("zfsprop")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	dataset := cfg.DatasetPath(name)

	out, err := exec.Command("zfs", "get", "-H", "-o", "value", "mountpoint", dataset).Output()
	require.NoError(t, err)
	mountpoint := strings.TrimSpace(string(out))
	assert.Equal(t, cfg.MountPoint(name), mountpoint, "mountpoint mismatch")

	out, _ = exec.Command("zfs", "get", "-H", "-o", "value", "compression", dataset).Output()
	compression := strings.TrimSpace(string(out))

	out, _ = exec.Command("zfs", "get", "-H", "-o", "value", "used", dataset).Output()
	used := strings.TrimSpace(string(out))
	assert.NotContains(t, []string{"0", "0B"}, used,
		"dataset used space should be non-zero — Docker should have written data")

	t.Logf("✓ ZFS properties: mountpoint=%s compression=%s used=%s", mountpoint, compression, used)
}

func TestCloneIsZFSClone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srcName := instName("zclone-src")
	cloneName := instName("zclone-dst")
	cleanup(t, cloneName)
	cleanup(t, srcName)
	// Defer order matters: defers run LIFO, so the LAST `defer cleanup(...)`
	// here runs FIRST. The clone's ZFS dataset depends on the source's
	// snapshot, so the clone must be destroyed first or `zfs destroy
	// <src>` fails (silently in cleanup) and the source leaks.
	defer cleanup(t, srcName)
	defer cleanup(t, cloneName)

	_, err := c.Create(ctx, srcName, nil, nil)
	require.NoError(t, err, "Create source")
	require.NoError(t, c.Snapshot(ctx, srcName, "golden", nil), "Snapshot")
	_, err = c.Clone(ctx, srcName, "golden", cloneName)
	require.NoError(t, err, "Clone")

	cloneDataset := cfg.DatasetPath(cloneName)
	out, err := exec.Command("zfs", "get", "-H", "-o", "value", "origin", cloneDataset).Output()
	require.NoError(t, err, "zfs get origin")
	origin := strings.TrimSpace(string(out))
	expectedOrigin := cfg.DatasetPath(srcName) + "@golden"
	assert.Equal(t, expectedOrigin, origin, "clone origin mismatch")

	out, _ = exec.Command("zfs", "get", "-H", "-o", "value", "-p", "used", cloneDataset).Output()

	t.Logf("✓ Clone %s origin=%s used=%s", cloneName, origin, strings.TrimSpace(string(out)))
}

func TestCloneParallelAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srcName := instName("src")
	cloneName := instName("clone")
	cleanup(t, cloneName)
	cleanup(t, srcName)
	// LIFO defer order — clone first so the source's snapshot has no
	// dependents at destroy time. See TestCloneIsZFSClone for context.
	defer cleanup(t, srcName)
	defer cleanup(t, cloneName)

	srcInst, err := c.Create(ctx, srcName, nil, nil)
	require.NoError(t, err, "Create source")

	markerPath := cfg.MountPoint(srcName) + "/.e2e-src-marker"
	require.NoError(t, os.WriteFile(markerPath, []byte("source-data"), 0644))

	require.NoError(t, c.Snapshot(ctx, srcName, "golden", nil), "Snapshot")

	cloneInst, err := c.Clone(ctx, srcName, "golden", cloneName)
	require.NoError(t, err, "Clone")

	assert.Equal(t, state.StatusRunning, cloneInst.Status)

	srcUnit := "dockersnap-" + srcName + ".service"
	cloneUnit := "dockersnap-" + cloneName + ".service"
	assert.True(t, systemdUnitActive(t, srcUnit), "source unit should be active")
	assert.True(t, systemdUnitActive(t, cloneUnit), "clone unit should be active")
	assert.True(t, dockerPing(t, cfg.SocketPath(srcName)), "source docker should respond")
	assert.True(t, dockerPing(t, cfg.SocketPath(cloneName)), "clone docker should respond")

	assert.NotEqual(t, srcInst.Subnet, cloneInst.Subnet, "source and clone must have different subnets")
	assert.True(t, netnsExists(t, "ds-"+srcName), "source netns should exist")
	assert.True(t, netnsExists(t, "ds-"+cloneName), "clone netns should exist")

	// CoW: marker exists in clone (inherited from snapshot).
	cloneMarker := cfg.MountPoint(cloneName) + "/.e2e-src-marker"
	data, err := os.ReadFile(cloneMarker)
	if assert.NoError(t, err, "clone should have inherited the source marker") {
		assert.Equal(t, "source-data", string(data))
	}

	// Write isolation: writes to clone don't propagate to source.
	require.NoError(t, os.WriteFile(cfg.MountPoint(cloneName)+"/.clone-only", []byte("x"), 0644))
	_, err = os.Stat(cfg.MountPoint(srcName) + "/.clone-only")
	assert.True(t, os.IsNotExist(err), "write to clone leaked to source — CoW broken!")

	assert.NotEmpty(t, cloneInst.CloneOf, "clone_of should be set on a clone")

	t.Logf("✓ Clone parallel: src=%s(%s) clone=%s(%s) isolation=OK",
		srcName, srcInst.Subnet, cloneName, cloneInst.Subnet)
}
