package instance

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/johnbuluba/dockersnap/internal/state"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// HealthCache caches the most recent health-poll result for each instance.
// Reads are non-blocking; writes happen on the poller's tick.
type HealthCache struct {
	mu      sync.RWMutex
	entries map[string]*HealthEntry
}

// HealthEntry is one cached health result.
type HealthEntry struct {
	Response         *pluginsdk.HealthResponse // nil for instances with no workload
	CheckedAt        time.Time
	ConsecutiveFails int
	Healthy          bool // surfaces "after threshold" state — flipped to false only after N consecutive fails
}

// NewHealthCache returns a fresh, empty cache.
func NewHealthCache() *HealthCache {
	return &HealthCache{entries: make(map[string]*HealthEntry)}
}

// Get returns the cached entry for an instance, or nil if not yet polled.
func (c *HealthCache) Get(name string) *HealthEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.entries[name]; ok {
		cp := *e
		return &cp
	}
	return nil
}

// Set stores the result of a health poll for an instance.
func (c *HealthCache) Set(name string, e *HealthEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[name] = e
}

// Delete removes the cached entry for an instance (e.g. on Delete).
func (c *HealthCache) Delete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, name)
}

// HealthPoller invokes plugin `health` periodically for every running
// instance with a workload bound, caching the result.
type HealthPoller struct {
	mgr              *Manager
	interval         time.Duration
	failureThreshold int
	cache            *HealthCache
	logger           *slog.Logger
}

// NewHealthPoller constructs a poller. The cache is exposed via Cache() so
// the API server can read from it.
func NewHealthPoller(mgr *Manager, interval time.Duration, failureThreshold int, logger *slog.Logger) *HealthPoller {
	if logger == nil {
		logger = slog.Default()
	}
	if failureThreshold < 1 {
		failureThreshold = 3
	}
	return &HealthPoller{
		mgr:              mgr,
		interval:         interval,
		failureThreshold: failureThreshold,
		cache:            NewHealthCache(),
		logger:           logger,
	}
}

// Cache returns the underlying cache for read access.
func (p *HealthPoller) Cache() *HealthCache { return p.cache }

// Run starts the polling loop. Blocks until ctx is cancelled.
func (p *HealthPoller) Run(ctx context.Context) {
	p.logger.Info("workload health poller started", "interval", p.interval.String())

	t := time.NewTicker(p.interval)
	defer t.Stop()

	// One immediate tick so the cache populates without waiting a full interval.
	p.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("workload health poller stopped")
			return
		case <-t.C:
			p.poll(ctx)
		}
	}
}

// poll iterates over running instances and updates the cache.
func (p *HealthPoller) poll(ctx context.Context) {
	instances, err := p.mgr.List(ctx)
	if err != nil {
		p.logger.Warn("health poller: list failed", "error", err)
		return
	}

	seen := make(map[string]bool, len(instances))
	for _, inst := range instances {
		seen[inst.Name] = true
		if inst.Status != state.StatusRunning || inst.WorkloadPlugin == "" {
			// Stopped or no workload — clear cache so stale data doesn't linger.
			p.cache.Delete(inst.Name)
			continue
		}
		p.pollOne(ctx, inst)
	}

	// Drop cache entries for instances that no longer exist.
	p.cache.mu.Lock()
	for name := range p.cache.entries {
		if !seen[name] {
			delete(p.cache.entries, name)
		}
	}
	p.cache.mu.Unlock()
}

func (p *HealthPoller) pollOne(ctx context.Context, inst *state.Instance) {
	resp, err := p.mgr.CallHealth(ctx, inst)
	now := time.Now()

	prev := p.cache.Get(inst.Name)
	consecutive := 0
	if prev != nil {
		consecutive = prev.ConsecutiveFails
	}

	if err != nil || resp == nil {
		consecutive++
		entry := &HealthEntry{
			Response:         resp,
			CheckedAt:        now,
			ConsecutiveFails: consecutive,
			Healthy:          consecutive < p.failureThreshold,
		}
		if entry.Healthy && resp == nil {
			// CallHealth returned (nil, nil) only when the instance has no
			// workload — already handled in poll(); shouldn't reach here.
			entry.Healthy = false
		}
		p.cache.Set(inst.Name, entry)
		p.logger.Debug("health poll failed", "instance", inst.Name,
			"consecutive_fails", consecutive, "error", err)
		return
	}

	if !resp.Healthy {
		consecutive++
		p.cache.Set(inst.Name, &HealthEntry{
			Response:         resp,
			CheckedAt:        now,
			ConsecutiveFails: consecutive,
			Healthy:          consecutive < p.failureThreshold,
		})
		return
	}

	// Healthy — reset the failure counter.
	p.cache.Set(inst.Name, &HealthEntry{
		Response:         resp,
		CheckedAt:        now,
		ConsecutiveFails: 0,
		Healthy:          true,
	})
}
