package proxy

import (
	"bufio"
	"context"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// cappedBuf collects up to cap bytes; writes past the cap are dropped so a
// runaway plugin or docker CLI can't blow out the daemon's memory. Only used
// for stderr capture from short-lived child processes.
type cappedBuf struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newCappedBuf(cap int) *cappedBuf { return &cappedBuf{cap: cap} }

func (b *cappedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	free := b.cap - len(b.buf)
	if free <= 0 {
		return len(p), nil
	}
	if len(p) > free {
		b.buf = append(b.buf, p[:free]...)
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *cappedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// EventListener watches Docker event streams for container start/stop events
// and triggers port scanning immediately when containers start.
type EventListener struct {
	mgr      *Manager
	lister   InstanceLister
	logger   *slog.Logger
	mu       sync.Mutex
	watchers map[string]context.CancelFunc
}

// NewEventListener creates a new event-driven port watcher.
func NewEventListener(mgr *Manager, lister InstanceLister, logger *slog.Logger) *EventListener {
	if logger == nil {
		logger = slog.Default()
	}
	return &EventListener{
		mgr:      mgr,
		lister:   lister,
		logger:   logger,
		watchers: make(map[string]context.CancelFunc),
	}
}

// Run starts event listening for all running instances and monitors for changes.
func (e *EventListener) Run(ctx context.Context) {
	e.logger.Info("event listener started")

	e.syncWatchers(ctx)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.stopAll()
			e.logger.Info("event listener stopped")
			return
		case <-ticker.C:
			e.syncWatchers(ctx)
		}
	}
}

func (e *EventListener) WatchInstance(ctx context.Context, info InstanceInfo) {
	e.mu.Lock()
	if _, exists := e.watchers[info.Name]; exists {
		e.mu.Unlock()
		return
	}

	watchCtx, cancel := context.WithCancel(ctx)
	e.watchers[info.Name] = cancel
	e.mu.Unlock()

	go e.watchEvents(watchCtx, info)
}

func (e *EventListener) UnwatchInstance(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if cancel, exists := e.watchers[name]; exists {
		cancel()
		delete(e.watchers, name)
		e.logger.Debug("stopped event watcher", "instance", name)
	}
}

func (e *EventListener) syncWatchers(ctx context.Context) {
	instances := e.lister.RunningInstances()

	running := make(map[string]InstanceInfo, len(instances))
	for _, inst := range instances {
		running[inst.Name] = inst
	}

	e.mu.Lock()
	for name, cancel := range e.watchers {
		if _, stillRunning := running[name]; !stillRunning {
			cancel()
			delete(e.watchers, name)
			e.logger.Debug("removed stale event watcher", "instance", name)
		}
	}
	e.mu.Unlock()

	for _, inst := range instances {
		e.WatchInstance(ctx, inst)
	}
}

func (e *EventListener) stopAll() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for name, cancel := range e.watchers {
		cancel()
		delete(e.watchers, name)
	}
}

// watchEvents runs `docker events` for a single instance and triggers port
// scanning when container start events occur. Bails out after too many
// consecutive failures so a misconfigured instance doesn't spin forever.
func (e *EventListener) watchEvents(ctx context.Context, info InstanceInfo) {
	log := e.logger.With("instance", info.Name)
	log.Debug("starting docker event listener", "socket", info.Socket)

	const maxConsecutiveFailures = 10
	failures := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if failures >= maxConsecutiveFailures {
			log.Warn("event listener disabled for instance after repeated failures",
				"failures", failures,
				"hint", "polling watcher will continue to discover ports as a fallback")
			return
		}

		// `--format` uses `.Action` (post-Docker-23 events.Message field).
		// Older docker had `.Status`, which now errors out at parse time and
		// makes docker exit before emitting any events — bumping the failure
		// counter on every retry until the listener gives up.
		cmd := exec.CommandContext(ctx, "docker", "-H", "unix://"+info.Socket,
			"events",
			"--filter", "type=container",
			"--filter", "event=start",
			"--format", "{{.Action}} {{.Actor.Attributes.name}}")

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Debug("event listener pipe error", "error", err)
			return
		}
		stderrBuf := newCappedBuf(4 * 1024)
		cmd.Stderr = stderrBuf

		if err := cmd.Start(); err != nil {
			failures++
			log.Debug("event listener start error, will retry",
				"error", err, "consecutive_failures", failures)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		// We managed to start docker events — reset the failure counter.
		gotEvent := false
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return
			default:
			}
			gotEvent = true

			line := scanner.Text()
			log.Debug("docker event", "line", line)

			// Small delay to let docker-proxy bind its port.
			time.Sleep(500 * time.Millisecond)
			if err := e.mgr.ScanAndForward(ctx, info.Name, info.Socket, info.NsIP); err != nil {
				log.Debug("event-triggered port scan failed", "error", err)
			}
		}

		waitErr := cmd.Wait()
		if gotEvent {
			failures = 0
		} else {
			failures++
			// Surface what `docker events` actually said when it exited
			// without ever emitting an event. Otherwise a regression in the
			// `--format` template (or a permanently-broken socket) just
			// counts silently up to the threshold.
			stderr := stderrBuf.String()
			if waitErr != nil || stderr != "" {
				log.Debug("docker events exited without emitting any event",
					"error", waitErr,
					"stderr", stderr,
					"consecutive_failures", failures)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
