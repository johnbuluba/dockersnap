package instance

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/config"
	"github.com/johnbuluba/dockersnap/internal/state"
	"github.com/johnbuluba/dockersnap/internal/zfs"
)

// mockDockerd implements DockerdManager for testing.
type mockDockerd struct {
	mu             sync.Mutex
	running        map[string]bool
	startErr       error
	stopErr        error
	removedConfigs map[string]bool
}

func newMockDockerd() *mockDockerd {
	return &mockDockerd{running: make(map[string]bool)}
}

func (m *mockDockerd) Start(_ context.Context, name, dataRoot, socket, pidFile, subnet string, dns []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.startErr != nil {
		return m.startErr
	}
	m.running[name] = true
	return nil
}

func (m *mockDockerd) Stop(_ context.Context, name, pidFile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopErr != nil {
		return m.stopErr
	}
	m.running[name] = false
	return nil
}

func (m *mockDockerd) nameFromPidFile(pidFile string) string {
	base := filepath.Base(pidFile)
	return base[:len(base)-4]
}

func (m *mockDockerd) IsRunning(pidFile string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[m.nameFromPidFile(pidFile)]
}

func (m *mockDockerd) IsRunningBatch(pidFiles []string) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]bool, len(pidFiles))
	for _, pf := range pidFiles {
		result[pf] = m.running[m.nameFromPidFile(pf)]
	}
	return result
}

func (m *mockDockerd) RemoveConfig(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.removedConfigs == nil {
		m.removedConfigs = map[string]bool{}
	}
	m.removedConfigs[name] = true
	return nil
}

func (m *mockDockerd) setStartErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startErr = err
}

func (m *mockDockerd) isStarted(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[name]
}

func testConfig(stateFile string) *config.Config {
	cfg := config.Defaults()
	cfg.StateFile = stateFile
	cfg.RunDir = "/tmp/dockersnap-test"
	return cfg
}

func testManager(t *testing.T) (*Manager, *zfs.Mock, *mockDockerd) {
	t.Helper()
	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "state.json")

	cfg := testConfig(stateFile)
	zfsMock := zfs.NewMock()
	dockerdMock := newMockDockerd()
	store := state.NewStore(stateFile)

	mgr, err := NewManagerWithOpts(cfg, ManagerOpts{
		ZFS:     zfsMock,
		Dockerd: dockerdMock,
		State:   store,
	})
	require.NoError(t, err)

	return mgr, zfsMock, dockerdMock
}

func TestManager_Create(t *testing.T) {
	mgr, zfsMock, dockerdMock := testManager(t)
	ctx := context.Background()

	inst, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)

	assert.Equal(t, "test1", inst.Name)
	assert.Equal(t, "dockersnap/instances/test1", inst.Dataset)
	assert.Equal(t, "10.10.0.0/16", inst.Subnet)
	assert.Equal(t, state.StatusRunning, inst.Status)

	assert.True(t, zfsMock.Datasets["dockersnap/instances/test1"], "ZFS dataset should be created")
	assert.True(t, dockerdMock.isStarted("test1"), "dockerd should be started")
}

func TestManager_Create_Duplicate(t *testing.T) {
	mgr, _, _ := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)

	_, err = mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	assert.Error(t, err)
}

func TestManager_Create_RejectsInvalidName(t *testing.T) {
	mgr, _, _ := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "../escape", WorkloadOpts{}, nil)
	assert.Error(t, err)
}

func TestManager_Create_RollsBackOnDockerdStartFailure(t *testing.T) {
	mgr, zfsMock, dockerdMock := testManager(t)
	ctx := context.Background()

	dockerdMock.setStartErr(assert.AnError)
	_, err := mgr.Create(ctx, "rolltest", WorkloadOpts{}, nil)
	require.Error(t, err)

	// Dataset destroyed, state empty
	assert.False(t, zfsMock.Datasets["dockersnap/instances/rolltest"],
		"ZFS dataset should have been destroyed after dockerd failure")
	insts, err := mgr.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, insts, "no instances should remain in state after rollback")
}

func TestManager_Delete(t *testing.T) {
	mgr, zfsMock, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)

	require.NoError(t, mgr.Delete(ctx, "test1", nil))

	assert.False(t, zfsMock.Datasets["dockersnap/instances/test1"], "ZFS dataset should be destroyed")
	assert.False(t, dockerdMock.isStarted("test1"), "dockerd should be stopped")
	assert.True(t, dockerdMock.removedConfigs["test1"],
		"daemon-configs/test1.json should be removed on Delete")

	instances, err := mgr.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, instances)
}

func TestManager_Get_StatusReflectsRuntime(t *testing.T) {
	mgr, _, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)

	got, err := mgr.Get(ctx, "test1")
	require.NoError(t, err)
	assert.Equal(t, state.StatusRunning, got.Status)

	// Simulate dockerd dying out-of-band (without going through Stop).
	dockerdMock.mu.Lock()
	dockerdMock.running["test1"] = false
	dockerdMock.mu.Unlock()

	got, err = mgr.Get(ctx, "test1")
	require.NoError(t, err)
	assert.Equal(t, state.StatusStopped, got.Status,
		"Get should report Stopped when runtime says dockerd is down, even if state file says running")
}

func TestManager_Snapshot(t *testing.T) {
	mgr, zfsMock, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)

	require.NoError(t, mgr.Snapshot(ctx, "test1", "golden", nil, nil))

	snaps := zfsMock.Snapshots["dockersnap/instances/test1"]
	assert.Equal(t, []string{"golden"}, snaps)
	assert.True(t, dockerdMock.isStarted("test1"), "dockerd should be running again after snapshot")
}

func TestManager_Snapshot_Duplicate(t *testing.T) {
	mgr, _, _ := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)
	require.NoError(t, mgr.Snapshot(ctx, "test1", "golden", nil, nil))

	err = mgr.Snapshot(ctx, "test1", "golden", nil, nil)
	assert.Error(t, err)
}

func TestManager_Revert(t *testing.T) {
	mgr, zfsMock, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)
	require.NoError(t, mgr.Snapshot(ctx, "test1", "golden", nil, nil))
	require.NoError(t, mgr.Snapshot(ctx, "test1", "later", nil, nil))

	require.NoError(t, mgr.Revert(ctx, "test1", "golden", RevertOpts{Force: true}, nil))

	assert.Equal(t, []string{"golden"}, zfsMock.Snapshots["dockersnap/instances/test1"],
		"rollback should drop snapshots taken after the target")
	assert.True(t, dockerdMock.isStarted("test1"), "dockerd should be running after revert")
}

func TestManager_Revert_NonexistentSnapshot(t *testing.T) {
	mgr, _, _ := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)

	err = mgr.Revert(ctx, "test1", "nonexistent", RevertOpts{}, nil)
	assert.Error(t, err)
}

func TestManager_Clone(t *testing.T) {
	mgr, zfsMock, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "source", WorkloadOpts{}, nil)
	require.NoError(t, err)
	require.NoError(t, mgr.Snapshot(ctx, "source", "golden", nil, nil))

	clone, err := mgr.Clone(ctx, "source", "golden", "clone1", nil)
	require.NoError(t, err)

	assert.Equal(t, "clone1", clone.Name)
	assert.Equal(t, "source@golden", clone.CloneOf)
	assert.Equal(t, "10.11.0.0/16", clone.Subnet, "clone should get the next subnet")

	assert.True(t, zfsMock.Datasets["dockersnap/instances/clone1"])
	assert.Equal(t, "dockersnap/instances/source@golden", zfsMock.Clones["dockersnap/instances/clone1"])
	assert.True(t, dockerdMock.isStarted("clone1"))
}

func TestManager_Clone_RollsBackOnDockerdStartFailure(t *testing.T) {
	mgr, zfsMock, _ := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "src", WorkloadOpts{}, nil)
	require.NoError(t, err)
	require.NoError(t, mgr.Snapshot(ctx, "src", "golden", nil, nil))

	// Inject a startup failure for the clone.
	mgr.dockerd.(*mockDockerd).setStartErr(assert.AnError)
	_, err = mgr.Clone(ctx, "src", "golden", "badclone", nil)
	require.Error(t, err)

	// Source still exists; clone dataset destroyed; clone removed from state.
	assert.True(t, zfsMock.Datasets["dockersnap/instances/src"], "source must survive clone failure")
	assert.False(t, zfsMock.Datasets["dockersnap/instances/badclone"],
		"clone dataset should have been destroyed")

	insts, err := mgr.List(ctx)
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.Equal(t, "src", insts[0].Name)
}

func TestManager_StartStop(t *testing.T) {
	mgr, _, dockerdMock := testManager(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "test1", WorkloadOpts{}, nil)
	require.NoError(t, err)
	require.True(t, dockerdMock.isStarted("test1"), "should be running after create")

	require.NoError(t, mgr.Stop(ctx, "test1", nil))
	assert.False(t, dockerdMock.isStarted("test1"))

	require.NoError(t, mgr.Start(ctx, "test1", nil))
	assert.True(t, dockerdMock.isStarted("test1"))
}

func TestManager_SubnetAllocation(t *testing.T) {
	mgr, _, _ := testManager(t)
	ctx := context.Background()

	inst1, err := mgr.Create(ctx, "first", WorkloadOpts{}, nil)
	require.NoError(t, err)
	inst2, err := mgr.Create(ctx, "second", WorkloadOpts{}, nil)
	require.NoError(t, err)
	inst3, err := mgr.Create(ctx, "third", WorkloadOpts{}, nil)
	require.NoError(t, err)

	assert.Equal(t, "10.10.0.0/16", inst1.Subnet)
	assert.Equal(t, "10.11.0.0/16", inst2.Subnet)
	assert.Equal(t, "10.12.0.0/16", inst3.Subnet)
}

// TestManager_ConcurrentCreate verifies that simultaneous Create calls each
// land on a unique subnet index.
func TestManager_ConcurrentCreate(t *testing.T) {
	mgr, _, _ := testManager(t)
	ctx := context.Background()

	const N = 8
	type result struct {
		inst *state.Instance
		err  error
	}
	results := make(chan result, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			inst, err := mgr.Create(ctx, "p"+strconv.Itoa(i), WorkloadOpts{}, nil)
			results <- result{inst, err}
		}()
	}

	subnets := make(map[string]int)
	for i := 0; i < N; i++ {
		r := <-results
		require.NoError(t, r.err, "concurrent Create failed")
		subnets[r.inst.Subnet]++
	}
	for s, count := range subnets {
		assert.LessOrEqual(t, count, 1, "subnet %s allocated %d times concurrently — race!", s, count)
	}
}
