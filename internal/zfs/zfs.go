package zfs

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// Commander defines the interface for ZFS operations.
// This allows mocking in tests.
type Commander interface {
	CreateDataset(ctx context.Context, dataset string) error
	DestroyDataset(ctx context.Context, dataset string, recursive bool) error
	Snapshot(ctx context.Context, dataset, label string) error
	Rollback(ctx context.Context, dataset, label string) error
	Clone(ctx context.Context, snapshot, target string) error
	ListSnapshots(ctx context.Context, dataset string) ([]string, error)
	DatasetExists(ctx context.Context, dataset string) (bool, error)
}

// CLI implements Commander by shelling out to the zfs command.
type CLI struct {
	logger *slog.Logger
}

// NewCLI creates a new ZFS CLI commander.
func NewCLI(logger *slog.Logger) *CLI {
	if logger == nil {
		logger = slog.Default()
	}
	return &CLI{logger: logger}
}

func (c *CLI) run(ctx context.Context, args ...string) (string, error) {
	c.logger.Debug("executing zfs command", "args", args)
	cmd := exec.CommandContext(ctx, "zfs", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("zfs %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// CreateDataset creates a new ZFS dataset.
func (c *CLI) CreateDataset(ctx context.Context, dataset string) error {
	_, err := c.run(ctx, "create", "-p", dataset)
	if err != nil {
		return fmt.Errorf("creating dataset %s: %w", dataset, err)
	}
	return nil
}

// DestroyDataset destroys a ZFS dataset, optionally recursively.
func (c *CLI) DestroyDataset(ctx context.Context, dataset string, recursive bool) error {
	args := []string{"destroy"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, dataset)

	_, err := c.run(ctx, args...)
	if err != nil {
		return fmt.Errorf("destroying dataset %s: %w", dataset, err)
	}
	return nil
}

// Snapshot creates a recursive snapshot of a dataset.
func (c *CLI) Snapshot(ctx context.Context, dataset, label string) error {
	snapName := fmt.Sprintf("%s@%s", dataset, label)
	_, err := c.run(ctx, "snapshot", "-r", snapName)
	if err != nil {
		return fmt.Errorf("creating snapshot %s: %w", snapName, err)
	}
	return nil
}

// Rollback rolls back a dataset to a snapshot, recursively destroying intermediate snapshots.
func (c *CLI) Rollback(ctx context.Context, dataset, label string) error {
	snapName := fmt.Sprintf("%s@%s", dataset, label)
	_, err := c.run(ctx, "rollback", "-r", snapName)
	if err != nil {
		return fmt.Errorf("rolling back to %s: %w", snapName, err)
	}
	return nil
}

// Clone creates a clone from a snapshot.
func (c *CLI) Clone(ctx context.Context, snapshot, target string) error {
	_, err := c.run(ctx, "clone", snapshot, target)
	if err != nil {
		return fmt.Errorf("cloning %s to %s: %w", snapshot, target, err)
	}
	return nil
}

// ListSnapshots returns all snapshot labels for a dataset.
func (c *CLI) ListSnapshots(ctx context.Context, dataset string) ([]string, error) {
	output, err := c.run(ctx, "list", "-t", "snapshot", "-H", "-o", "name", "-r", dataset)
	if err != nil {
		// No snapshots returns an error on some systems
		if strings.Contains(err.Error(), "no datasets") {
			return nil, nil
		}
		return nil, fmt.Errorf("listing snapshots for %s: %w", dataset, err)
	}

	if output == "" {
		return nil, nil
	}

	var labels []string
	for _, line := range strings.Split(output, "\n") {
		// Format: pool/dataset@label
		parts := strings.SplitN(line, "@", 2)
		if len(parts) == 2 && parts[0] == dataset {
			labels = append(labels, parts[1])
		}
	}
	return labels, nil
}

// DatasetExists checks if a ZFS dataset exists.
func (c *CLI) DatasetExists(ctx context.Context, dataset string) (bool, error) {
	_, err := c.run(ctx, "list", "-H", "-o", "name", dataset)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return false, nil
		}
		return false, fmt.Errorf("checking dataset %s: %w", dataset, err)
	}
	return true, nil
}
