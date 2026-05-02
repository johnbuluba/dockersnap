package zfs

import (
	"context"
	"fmt"
	"sync"
)

// Mock implements Commander for testing without actual ZFS.
type Mock struct {
	mu        sync.Mutex
	Datasets  map[string]bool
	Snapshots map[string][]string // dataset -> snapshot labels
	Clones    map[string]string   // clone dataset -> source snapshot

	// Error injection
	CreateErr   error
	DestroyErr  error
	SnapshotErr error
	RollbackErr error
	CloneErr    error
}

// NewMock creates a new ZFS mock.
func NewMock() *Mock {
	return &Mock{
		Datasets:  make(map[string]bool),
		Snapshots: make(map[string][]string),
		Clones:    make(map[string]string),
	}
}

func (m *Mock) CreateDataset(_ context.Context, dataset string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.CreateErr != nil {
		return m.CreateErr
	}
	if m.Datasets[dataset] {
		return fmt.Errorf("dataset %s already exists", dataset)
	}
	m.Datasets[dataset] = true
	return nil
}

func (m *Mock) DestroyDataset(_ context.Context, dataset string, recursive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.DestroyErr != nil {
		return m.DestroyErr
	}
	if !m.Datasets[dataset] {
		return fmt.Errorf("dataset %s does not exist", dataset)
	}
	delete(m.Datasets, dataset)
	delete(m.Snapshots, dataset)
	return nil
}

func (m *Mock) Snapshot(_ context.Context, dataset, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SnapshotErr != nil {
		return m.SnapshotErr
	}
	if !m.Datasets[dataset] {
		return fmt.Errorf("dataset %s does not exist", dataset)
	}
	m.Snapshots[dataset] = append(m.Snapshots[dataset], label)
	return nil
}

func (m *Mock) Rollback(_ context.Context, dataset, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.RollbackErr != nil {
		return m.RollbackErr
	}
	if !m.Datasets[dataset] {
		return fmt.Errorf("dataset %s does not exist", dataset)
	}

	snaps := m.Snapshots[dataset]
	found := false
	var remaining []string
	for _, s := range snaps {
		remaining = append(remaining, s)
		if s == label {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("snapshot %s@%s does not exist", dataset, label)
	}
	m.Snapshots[dataset] = remaining
	return nil
}

func (m *Mock) Clone(_ context.Context, snapshot, target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.CloneErr != nil {
		return m.CloneErr
	}
	if m.Datasets[target] {
		return fmt.Errorf("target dataset %s already exists", target)
	}
	m.Datasets[target] = true
	m.Clones[target] = snapshot
	return nil
}

func (m *Mock) ListSnapshots(_ context.Context, dataset string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.Snapshots[dataset], nil
}

func (m *Mock) DatasetExists(_ context.Context, dataset string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.Datasets[dataset], nil
}
