package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Status is the lifecycle status of an instance.
type Status string

const (
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusError   Status = "error"
)

// SnapshotInfo holds metadata about a snapshot.
type SnapshotInfo struct {
	Label     string            `json:"label"`
	CreatedAt time.Time         `json:"created_at"`
	Tags      map[string]string `json:"tags,omitempty"`
}

// Instance represents the persistent state of a single dockersnap instance.
type Instance struct {
	Name        string         `json:"name"`
	Dataset     string         `json:"dataset"`
	Subnet      string         `json:"subnet"`
	SubnetIndex int            `json:"subnet_index"`
	MetalLBIP   string         `json:"metallb_ip"`
	Socket      string         `json:"socket"`
	PidFile     string         `json:"pid_file"`
	CreatedAt   time.Time      `json:"created_at"`
	Status      Status         `json:"status"`
	CloneOf     string         `json:"clone_of,omitempty"`
	Snapshots   []SnapshotInfo `json:"snapshots"`

	// Workload binding. Set on Create; immutable afterward (Revert/Snapshot/
	// Clone never rewrite). Empty WorkloadPlugin means "no workload, plain
	// Docker environment".
	WorkloadPlugin          string                 `json:"workload_plugin,omitempty"`
	WorkloadConfig          map[string]interface{} `json:"workload_config,omitempty"`
	WorkloadContractVersion string                 `json:"workload_contract_version,omitempty"`
}

// SnapshotLabels returns just the snapshot labels.
func (inst *Instance) SnapshotLabels() []string {
	labels := make([]string, len(inst.Snapshots))
	for i, s := range inst.Snapshots {
		labels[i] = s.Label
	}
	return labels
}

// HasSnapshot returns true if the instance has a snapshot with the given label.
func (inst *Instance) HasSnapshot(label string) bool {
	for _, s := range inst.Snapshots {
		if s.Label == label {
			return true
		}
	}
	return false
}

// GetSnapshot returns the snapshot info for a label, or nil if not found.
func (inst *Instance) GetSnapshot(label string) *SnapshotInfo {
	for i := range inst.Snapshots {
		if inst.Snapshots[i].Label == label {
			return &inst.Snapshots[i]
		}
	}
	return nil
}

// UnmarshalJSON provides backward compatibility: old state files have
// "snapshots" as []string, new ones have []SnapshotInfo.
func (inst *Instance) UnmarshalJSON(data []byte) error {
	type Alias Instance
	aux := &struct {
		Snapshots json.RawMessage `json:"snapshots"`
		*Alias
	}{
		Alias: (*Alias)(inst),
	}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	if len(aux.Snapshots) > 0 {
		var infos []SnapshotInfo
		if err := json.Unmarshal(aux.Snapshots, &infos); err == nil {
			inst.Snapshots = infos
			return nil
		}
		var labels []string
		if err := json.Unmarshal(aux.Snapshots, &labels); err == nil {
			inst.Snapshots = make([]SnapshotInfo, len(labels))
			for i, l := range labels {
				inst.Snapshots[i] = SnapshotInfo{Label: l}
			}
			return nil
		}
	}
	return nil
}

// State represents the full persistent state of dockersnap.
type State struct {
	Instances       map[string]*Instance `json:"instances"`
	NextSubnetIndex int                  `json:"next_subnet_index"`
}

// NextFreeSubnetIndex finds the lowest subnet index not currently used by any instance.
func (s *State) NextFreeSubnetIndex() int {
	if len(s.Instances) == 0 {
		return 0
	}

	used := make(map[int]bool, len(s.Instances))
	for _, inst := range s.Instances {
		used[inst.SubnetIndex] = true
	}

	for i := 0; i < s.NextSubnetIndex+1; i++ {
		if !used[i] {
			return i
		}
	}

	return s.NextSubnetIndex
}

// Validate checks that the state is internally consistent.
// Returns an error for any instance with missing required fields.
func (s *State) Validate() error {
	for name, inst := range s.Instances {
		if inst == nil {
			return fmt.Errorf("instance %q is nil", name)
		}
		if inst.Name == "" {
			return fmt.Errorf("instance %q has empty Name", name)
		}
		if inst.Name != name {
			return fmt.Errorf("instance map key %q does not match Name %q", name, inst.Name)
		}
		if inst.Dataset == "" {
			return fmt.Errorf("instance %q has empty Dataset", name)
		}
		if inst.Subnet == "" {
			return fmt.Errorf("instance %q has empty Subnet", name)
		}
	}
	return nil
}

// Store manages reading and writing the state file.
// All mutations should go through Update to ensure atomic load-modify-save.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore creates a new state store at the given path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load reads the state from disk. Returns empty state if file doesn't exist.
// Callers performing a read-modify-write cycle should use Update instead.
func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (*State, error) {
	state := &State{
		Instances: make(map[string]*Instance),
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, fmt.Errorf("reading state file %s: %w", s.path, err)
	}

	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("parsing state file %s: %w", s.path, err)
	}

	if state.Instances == nil {
		state.Instances = make(map[string]*Instance)
	}

	if err := state.Validate(); err != nil {
		return nil, fmt.Errorf("invalid state file %s: %w", s.path, err)
	}

	return state, nil
}

// Save writes the state to disk atomically (write to temp, then rename).
// Callers performing a read-modify-write cycle should use Update instead.
func (s *Store) Save(state *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(state)
}

func (s *Store) saveLocked(state *State) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating state directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("writing temp state file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("renaming state file: %w", err)
	}

	return nil
}

// Update atomically loads the state, applies fn, and saves it back.
// The store mutex is held for the entire load-modify-save cycle, preventing
// concurrent operations from clobbering each other.
//
// If fn returns a non-nil error, the state is NOT saved and the error is returned.
// If fn mutates the state but returns nil, the state is saved.
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}

	if err := fn(state); err != nil {
		return err
	}

	return s.saveLocked(state)
}

// View atomically loads the state and passes it to fn. The state is NOT saved.
// Use this for read-only queries that need a consistent snapshot.
func (s *Store) View(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return err
	}
	return fn(state)
}
