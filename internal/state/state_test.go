package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_LoadSave(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "state.json")

	store := NewStore(path)

	// Load from non-existent file should return empty state
	st, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, st.Instances)

	// Save state
	st.Instances["test"] = &Instance{
		Name:      "test",
		Dataset:   "pool/instances/test",
		Subnet:    "10.10.0.0/16",
		MetalLBIP: "10.10.10.10",
		Socket:    "/run/dockersnap/test.sock",
		PidFile:   "/run/dockersnap/test.pid",
		CreatedAt: time.Now(),
		Status:    StatusRunning,
		Snapshots: []SnapshotInfo{{Label: "golden", CreatedAt: time.Now()}},
	}
	st.NextSubnetIndex = 1

	require.NoError(t, store.Save(st))

	_, err = os.Stat(path)
	require.NoError(t, err, "state file should exist")

	// Reload and verify
	st2, err := store.Load()
	require.NoError(t, err)
	require.Len(t, st2.Instances, 1)
	assert.Equal(t, "test", st2.Instances["test"].Name)
	assert.Equal(t, "10.10.0.0/16", st2.Instances["test"].Subnet)
	assert.Equal(t, 1, st2.NextSubnetIndex)
	require.Len(t, st2.Instances["test"].Snapshots, 1)
	assert.Equal(t, "golden", st2.Instances["test"].Snapshots[0].Label)
}

func TestStore_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "subdir", "state.json")

	store := NewStore(path)

	st := &State{
		Instances:       make(map[string]*Instance),
		NextSubnetIndex: 0,
	}

	// Should create parent directories
	require.NoError(t, store.Save(st))

	tmpPath := path + ".tmp"
	_, err := os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(err), "temp file should not exist after save")
}

func TestState_NextFreeSubnetIndex(t *testing.T) {
	tests := []struct {
		name     string
		state    *State
		expected int
	}{
		{
			name: "empty state returns 0",
			state: &State{
				Instances:       map[string]*Instance{},
				NextSubnetIndex: 0,
			},
			expected: 0,
		},
		{
			name: "one instance using index 0, returns 1",
			state: &State{
				Instances: map[string]*Instance{
					"a": {Name: "a", SubnetIndex: 0},
				},
				NextSubnetIndex: 1,
			},
			expected: 1,
		},
		{
			name: "gap at index 0 (deleted first instance)",
			state: &State{
				Instances: map[string]*Instance{
					"b": {Name: "b", SubnetIndex: 1},
					"c": {Name: "c", SubnetIndex: 2},
				},
				NextSubnetIndex: 3,
			},
			expected: 0,
		},
		{
			name: "gap in middle",
			state: &State{
				Instances: map[string]*Instance{
					"a": {Name: "a", SubnetIndex: 0},
					"c": {Name: "c", SubnetIndex: 2},
				},
				NextSubnetIndex: 3,
			},
			expected: 1,
		},
		{
			name: "no gaps, all sequential",
			state: &State{
				Instances: map[string]*Instance{
					"a": {Name: "a", SubnetIndex: 0},
					"b": {Name: "b", SubnetIndex: 1},
					"c": {Name: "c", SubnetIndex: 2},
				},
				NextSubnetIndex: 3,
			},
			expected: 3,
		},
		{
			name: "multiple gaps, returns lowest",
			state: &State{
				Instances: map[string]*Instance{
					"b": {Name: "b", SubnetIndex: 1},
					"d": {Name: "d", SubnetIndex: 3},
				},
				NextSubnetIndex: 4,
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.NextFreeSubnetIndex())
		})
	}
}

func TestInstance_UnmarshalJSON_BackwardCompat(t *testing.T) {
	// Old format: snapshots as []string
	oldJSON := `{
		"name": "test",
		"dataset": "pool/test",
		"subnet": "10.10.0.0/16",
		"subnet_index": 0,
		"metallb_ip": "10.10.10.10",
		"socket": "/run/dockersnap/test.sock",
		"pid_file": "/run/dockersnap/test.pid",
		"created_at": "2026-01-01T00:00:00Z",
		"status": "running",
		"snapshots": ["golden", "snap2"]
	}`

	var inst Instance
	require.NoError(t, json.Unmarshal([]byte(oldJSON), &inst))
	require.Len(t, inst.Snapshots, 2)
	assert.Equal(t, "golden", inst.Snapshots[0].Label)
	assert.Equal(t, "snap2", inst.Snapshots[1].Label)

	// New format: snapshots as []SnapshotInfo
	newJSON := `{
		"name": "test2",
		"dataset": "pool/test2",
		"subnet": "10.11.0.0/16",
		"subnet_index": 1,
		"metallb_ip": "10.11.10.10",
		"socket": "/run/dockersnap/test2.sock",
		"pid_file": "/run/dockersnap/test2.pid",
		"created_at": "2026-01-01T00:00:00Z",
		"status": "running",
		"snapshots": [{"label":"golden","created_at":"2026-01-01T12:00:00Z","tags":{"version":"1.0"}}]
	}`

	var inst2 Instance
	require.NoError(t, json.Unmarshal([]byte(newJSON), &inst2))
	require.Len(t, inst2.Snapshots, 1)
	assert.Equal(t, "golden", inst2.Snapshots[0].Label)
	assert.Equal(t, "1.0", inst2.Snapshots[0].Tags["version"])
}

func TestState_Validate(t *testing.T) {
	tests := []struct {
		name    string
		state   *State
		wantErr string
	}{
		{
			name:  "empty state is valid",
			state: &State{Instances: map[string]*Instance{}},
		},
		{
			name: "valid instance",
			state: &State{Instances: map[string]*Instance{
				"a": {Name: "a", Dataset: "p/a", Subnet: "10.0.0.0/16"},
			}},
		},
		{
			name: "nil instance",
			state: &State{Instances: map[string]*Instance{
				"a": nil,
			}},
			wantErr: "is nil",
		},
		{
			name: "empty Name",
			state: &State{Instances: map[string]*Instance{
				"a": {Dataset: "p/a", Subnet: "10.0.0.0/16"},
			}},
			wantErr: "empty Name",
		},
		{
			name: "key/Name mismatch",
			state: &State{Instances: map[string]*Instance{
				"a": {Name: "b", Dataset: "p/b", Subnet: "10.0.0.0/16"},
			}},
			wantErr: "does not match Name",
		},
		{
			name: "empty Dataset",
			state: &State{Instances: map[string]*Instance{
				"a": {Name: "a", Subnet: "10.0.0.0/16"},
			}},
			wantErr: "empty Dataset",
		},
		{
			name: "empty Subnet",
			state: &State{Instances: map[string]*Instance{
				"a": {Name: "a", Dataset: "p/a"},
			}},
			wantErr: "empty Subnet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.state.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
