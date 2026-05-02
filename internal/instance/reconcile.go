package instance

import (
	"context"
	"log/slog"

	"github.com/johnbuluba/dockersnap/internal/state"
)

// Reconcile checks the state file against reality and fixes discrepancies.
// Called on daemon startup to restart dockerd processes that should be running
// and re-establish port proxies for already-running instances.
func (m *Manager) Reconcile(ctx context.Context) error {
	m.logger.Info("reconciling instance state")

	// Load a snapshot of the current instances under the lock, then act on it
	// outside the lock to avoid holding it across slow ZFS/dockerd operations.
	var instances []*state.Instance
	if err := m.state.View(func(st *state.State) error {
		for _, i := range st.Instances {
			snap := *i
			instances = append(instances, &snap)
		}
		return nil
	}); err != nil {
		return err
	}

	// Batch the IsRunning probe so we issue a single systemctl call instead of N.
	pidFiles := make([]string, 0, len(instances))
	for _, inst := range instances {
		pidFiles = append(pidFiles, inst.PidFile)
	}
	running := m.dockerd.IsRunningBatch(pidFiles)

	var started, proxied, failed, skipped int
	statusUpdates := make(map[string]state.Status)

	for _, inst := range instances {
		log := m.logger.With("instance", inst.Name)

		exists, err := m.zfs.DatasetExists(ctx, inst.Dataset)
		if err != nil {
			log.Error("failed to check dataset existence", "error", err)
			statusUpdates[inst.Name] = state.StatusError
			failed++
			continue
		}
		if !exists {
			log.Warn("dataset does not exist, marking instance as error")
			statusUpdates[inst.Name] = state.StatusError
			failed++
			continue
		}

		isRunning := running[inst.PidFile]
		switch {
		case inst.Status == state.StatusRunning && !isRunning:
			log.Info("instance should be running but dockerd is not, starting")
			if err := m.restartDockerd(ctx, inst); err != nil {
				log.Error("failed to start dockerd during reconciliation", "error", err)
				statusUpdates[inst.Name] = state.StatusError
				failed++
				continue
			}
			started++
		case inst.Status == state.StatusRunning && isRunning:
			log.Info("instance already running, re-establishing port proxies")
			m.SetupPortForwarding(ctx, inst)
			proxied++
		case inst.Status == state.StatusStopped:
			log.Debug("instance is stopped, skipping")
			skipped++
		default:
			skipped++
		}
	}

	if len(statusUpdates) > 0 {
		if err := m.state.Update(func(st *state.State) error {
			for name, status := range statusUpdates {
				if i, ok := st.Instances[name]; ok {
					i.Status = status
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	m.logger.Info("reconciliation complete",
		slog.Int("started", started),
		slog.Int("proxied", proxied),
		slog.Int("failed", failed),
		slog.Int("skipped", skipped),
	)
	return nil
}
