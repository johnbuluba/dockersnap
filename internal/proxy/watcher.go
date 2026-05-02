package proxy

import (
	"context"
	"log/slog"
	"time"
)

// InstanceInfo provides the information the watcher needs about a running instance.
type InstanceInfo struct {
	Name   string
	Socket string
	NsIP   string
}

// InstanceLister is implemented by the instance manager to list running instances.
type InstanceLister interface {
	RunningInstances() []InstanceInfo
}

// Watcher periodically scans running instances for new published ports
// and auto-forwards them through the proxy. This eliminates the need
// for manual /ports/refresh calls after kind cluster creation.
type Watcher struct {
	mgr      *Manager
	lister   InstanceLister
	interval time.Duration
	logger   *slog.Logger
}

// NewWatcher creates a background port discovery watcher.
// interval controls how often it scans (e.g., 5s).
func NewWatcher(mgr *Manager, lister InstanceLister, interval time.Duration, logger *slog.Logger) *Watcher {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		mgr:      mgr,
		lister:   lister,
		interval: interval,
		logger:   logger,
	}
}

// Run starts the watcher loop. It blocks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	w.logger.Info("port watcher started", "interval", w.interval.String())

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("port watcher stopped")
			return
		case <-ticker.C:
			w.scan(ctx)
		}
	}
}

// scan checks all running instances for new port bindings.
func (w *Watcher) scan(ctx context.Context) {
	instances := w.lister.RunningInstances()
	for _, inst := range instances {
		// Check if we already have ports for this instance
		existing := w.mgr.ListPorts(inst.Name)
		if len(existing.Ports) > 0 {
			// Already forwarding — skip scan (use /ports/refresh for manual rescan)
			continue
		}

		// No ports forwarded yet — scan for new containers
		if err := w.mgr.ScanAndForward(ctx, inst.Name, inst.Socket, inst.NsIP); err != nil {
			w.logger.Debug("port watcher scan failed", "instance", inst.Name, "error", err)
		}
	}
}
