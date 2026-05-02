package zfs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMock_CreateAndDestroy(t *testing.T) {
	m := NewMock()
	ctx := context.Background()

	require.NoError(t, m.CreateDataset(ctx, "pool/test"))

	// Duplicate create should fail
	require.Error(t, m.CreateDataset(ctx, "pool/test"), "expected error for duplicate create")

	exists, err := m.DatasetExists(ctx, "pool/test")
	require.NoError(t, err)
	assert.True(t, exists)

	require.NoError(t, m.DestroyDataset(ctx, "pool/test", true))

	exists, _ = m.DatasetExists(ctx, "pool/test")
	assert.False(t, exists, "dataset should not exist after destroy")
}

func TestMock_SnapshotAndRollback(t *testing.T) {
	m := NewMock()
	ctx := context.Background()

	require.NoError(t, m.CreateDataset(ctx, "pool/test"))

	require.NoError(t, m.Snapshot(ctx, "pool/test", "snap1"))
	require.NoError(t, m.Snapshot(ctx, "pool/test", "snap2"))

	snaps, err := m.ListSnapshots(ctx, "pool/test")
	require.NoError(t, err)
	require.Len(t, snaps, 2)

	// Rollback to snap1 should remove snap2
	require.NoError(t, m.Rollback(ctx, "pool/test", "snap1"))

	snaps, _ = m.ListSnapshots(ctx, "pool/test")
	assert.Equal(t, []string{"snap1"}, snaps)
}

func TestMock_Clone(t *testing.T) {
	m := NewMock()
	ctx := context.Background()

	require.NoError(t, m.CreateDataset(ctx, "pool/source"))
	require.NoError(t, m.Snapshot(ctx, "pool/source", "snap1"))

	require.NoError(t, m.Clone(ctx, "pool/source@snap1", "pool/clone1"))

	exists, _ := m.DatasetExists(ctx, "pool/clone1")
	assert.True(t, exists, "clone dataset should exist")

	assert.Equal(t, "pool/source@snap1", m.Clones["pool/clone1"])
}
