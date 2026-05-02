package instance

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/state"
)

// TestReconcile_RestartsOrphanedRunningInstance simulates a daemon crash
// where the state file says an instance is running but dockerd is dead.
// Reconcile should bring it back.
func TestReconcile_RestartsOrphanedRunningInstance(t *testing.T) {
	mgr, _, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", WorkloadOpts{}, nil)
	require.NoError(t, err)

	// Simulate crash: dockerd dies out-of-band but state still says "running".
	dockerdMock.mu.Lock()
	dockerdMock.running["env1"] = false
	dockerdMock.mu.Unlock()

	require.NoError(t, mgr.Reconcile(ctx))

	assert.True(t, dockerdMock.isStarted("env1"),
		"reconcile should restart dockerd for instances marked running in state")
}

// TestReconcile_LeavesStoppedAlone — instances that the state file says are
// stopped should not be auto-started.
func TestReconcile_LeavesStoppedAlone(t *testing.T) {
	mgr, _, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", WorkloadOpts{}, nil)
	require.NoError(t, err)
	require.NoError(t, mgr.Stop(ctx, "env1", nil))
	require.False(t, dockerdMock.isStarted("env1"))

	require.NoError(t, mgr.Reconcile(ctx))

	assert.False(t, dockerdMock.isStarted("env1"),
		"reconcile must not start instances marked stopped")
}

// TestReconcile_MarksMissingDatasetAsError — if the ZFS dataset has been
// removed externally, the instance is unrecoverable; reconcile persists the
// error status in the state file. (Manager.Get recomputes status from
// dockerd liveness, so we inspect the persistent state directly.)
func TestReconcile_MarksMissingDatasetAsError(t *testing.T) {
	mgr, zfsMock, _ := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", WorkloadOpts{}, nil)
	require.NoError(t, err)

	// Simulate dataset disappeared (zpool destroyed, manual zfs destroy, etc.).
	delete(zfsMock.Datasets, "dockersnap/instances/env1")

	require.NoError(t, mgr.Reconcile(ctx))

	// Read the persistent state (bypasses Manager.Get's runtime-status override).
	st, err := mgr.state.Load()
	require.NoError(t, err)
	require.Contains(t, st.Instances, "env1")
	assert.Equal(t, state.StatusError, st.Instances["env1"].Status,
		"missing dataset should be persisted as error in state")
}

// TestReconcile_AlreadyRunningInstanceIsLeftAlone — reconcile should not
// restart an instance that is already running, but should re-establish port
// forwarding (which lives in memory and is lost on daemon restart).
func TestReconcile_AlreadyRunningInstanceIsLeftAlone(t *testing.T) {
	mgr, _, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", WorkloadOpts{}, nil)
	require.NoError(t, err)
	require.True(t, dockerdMock.isStarted("env1"))

	// Reconcile should NOT call Start again on a running instance.
	// We can't directly observe "Start was not called" with the current mock,
	// but we can verify the instance is still running afterwards and didn't
	// transition through any error state.
	require.NoError(t, mgr.Reconcile(ctx))

	got, err := mgr.Get(ctx, "env1")
	require.NoError(t, err)
	assert.Equal(t, state.StatusRunning, got.Status)
}
