package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// Status is the discovered/init state of a plugin.
type Status string

const (
	StatusReady    Status = "ready"
	StatusDisabled Status = "disabled" // schema or init failed; plugin not usable
)

// Plugin is one discovered plugin entry in the Manager.
type Plugin struct {
	Name         string                   // file name, e.g. "kind"
	Path         string                   // absolute path to the binary
	Schema       pluginsdk.SchemaResponse // cached from `<plugin> schema`
	Status       Status
	Error        string // populated when Status == StatusDisabled
	SchemaDigest string // sha256 hex of the schema JSON
	BinaryDigest string // sha256 hex of the binary file
	LoadedAt     time.Time
}

// Timeouts maps command names to per-call timeouts. Unset entries get the
// fallback duration.
type Timeouts struct {
	Init     time.Duration
	Schema   time.Duration
	Validate time.Duration
	Deploy   time.Duration
	Teardown time.Duration
	Access   time.Duration
	Describe time.Duration
	Health   time.Duration
}

// DefaultTimeouts mirrors the defaults documented in PLUGIN-DESIGN.md §5.
func DefaultTimeouts() Timeouts {
	return Timeouts{
		Init:     10 * time.Second,
		Schema:   5 * time.Second,
		Validate: 30 * time.Second,
		Deploy:   600 * time.Second,
		Teardown: 120 * time.Second,
		Access:   10 * time.Second,
		Describe: 10 * time.Second,
		Health:   10 * time.Second,
	}
}

// timeoutFor returns the configured timeout for a command, or 30s as a
// final fallback for commands not in the table.
func (t Timeouts) timeoutFor(command string) time.Duration {
	switch command {
	case "init":
		return nonzero(t.Init, 10*time.Second)
	case "schema":
		return nonzero(t.Schema, 5*time.Second)
	case "validate":
		return nonzero(t.Validate, 30*time.Second)
	case "deploy":
		return nonzero(t.Deploy, 600*time.Second)
	case "teardown":
		return nonzero(t.Teardown, 120*time.Second)
	case "access":
		return nonzero(t.Access, 10*time.Second)
	case "describe":
		return nonzero(t.Describe, 10*time.Second)
	case "health":
		return nonzero(t.Health, 10*time.Second)
	}
	return 30 * time.Second
}

func nonzero(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

// Manager discovers plugins under a directory and exposes methods to invoke
// their commands.
type Manager struct {
	dir      string
	timeouts Timeouts
	logger   *slog.Logger

	mu      sync.RWMutex
	plugins map[string]*Plugin
}

// NewManager constructs a Manager. Discover() must be called before plugins
// are usable.
func NewManager(dir string, timeouts Timeouts, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		dir:      dir,
		timeouts: timeouts,
		logger:   logger,
		plugins:  make(map[string]*Plugin),
	}
}

// Discover scans the plugin dir, runs schema + init for each entry, and
// populates the cache. Existing entries are replaced.
func (m *Manager) Discover(ctx context.Context) error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			m.logger.Info("plugin dir does not exist — no plugins loaded", "dir", m.dir)
			m.mu.Lock()
			m.plugins = make(map[string]*Plugin)
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("reading plugin dir %s: %w", m.dir, err)
	}

	next := make(map[string]*Plugin)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(m.dir, name)

		info, err := entry.Info()
		if err != nil {
			m.logger.Warn("plugin stat failed; skipping", "name", name, "error", err)
			continue
		}
		if info.Mode()&0111 == 0 {
			m.logger.Debug("plugin file is not executable; skipping", "name", name)
			continue
		}

		p := &Plugin{Name: name, Path: path, LoadedAt: time.Now()}
		if err := m.loadOne(ctx, p); err != nil {
			p.Status = StatusDisabled
			p.Error = err.Error()
			m.logger.Warn("plugin disabled", "name", name, "error", err)
		} else {
			p.Status = StatusReady
			m.logger.Info("plugin ready",
				"name", name,
				"version", p.Schema.PluginVersion,
				"contract", p.Schema.SupportedContractVersions)
		}
		next[name] = p
	}

	m.mu.Lock()
	m.plugins = next
	m.mu.Unlock()
	return nil
}

// Reload re-runs Discover, clearing the existing cache.
func (m *Manager) Reload(ctx context.Context) error {
	return m.Discover(ctx)
}

// Get returns a plugin by name. Returns an error if the plugin is unknown
// or disabled.
func (m *Manager) Get(name string) (*Plugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.plugins[name]
	if !ok {
		return nil, fmt.Errorf("plugin %q not found", name)
	}
	if p.Status != StatusReady {
		return nil, fmt.Errorf("plugin %q is %s: %s", name, p.Status, p.Error)
	}
	return p, nil
}

// List returns all known plugins (ready and disabled), ordered by name.
func (m *Manager) List() []*Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Plugin, 0, len(m.plugins))
	for _, p := range m.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// loadOne runs schema then init on a single plugin. Mutates p in place.
func (m *Manager) loadOne(ctx context.Context, p *Plugin) error {
	digest, err := fileDigest(p.Path)
	if err != nil {
		return fmt.Errorf("hashing binary: %w", err)
	}
	p.BinaryDigest = digest

	// schema
	schemaCtx, cancel := context.WithTimeout(ctx, m.timeouts.timeoutFor("schema"))
	defer cancel()
	out, err := m.runRaw(schemaCtx, p.Path, "schema", nil)
	if err != nil {
		return fmt.Errorf("running schema: %w", err)
	}
	if err := json.Unmarshal(out, &p.Schema); err != nil {
		return fmt.Errorf("parsing schema response: %w", err)
	}
	if !contractCompatible(p.Schema.SupportedContractVersions) {
		return fmt.Errorf("plugin contract versions %v are not supported by this daemon",
			p.Schema.SupportedContractVersions)
	}
	p.SchemaDigest = sha256Hex(out)

	// init (optional — empty exit-0 means "no init needed")
	initCtx, cancel2 := context.WithTimeout(ctx, m.timeouts.timeoutFor("init"))
	defer cancel2()
	if _, err := m.runRaw(initCtx, p.Path, "init", nil); err != nil {
		return fmt.Errorf("running init: %w", err)
	}

	return nil
}

// supportedContractVersions is the set of contract versions this daemon
// knows. We only ship "1" today.
var supportedContractVersions = map[string]bool{"1": true}

func contractCompatible(plugin []string) bool {
	for _, v := range plugin {
		if supportedContractVersions[v] {
			return true
		}
	}
	return false
}

// fileDigest returns the sha256 hex digest of a file's contents.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// runRaw invokes a plugin command with the given stdin payload (may be nil)
// and returns stdout. Stderr is parsed line-by-line as pluginsdk.LogEntry
// NDJSON and re-emitted into the daemon's slog; non-JSON lines are logged
// verbatim at INFO. The raw stream is also retained (capped) so error paths
// can include it in the wrapped error.
//
// command is what gets passed as os.Args[1] to the plugin binary; payload
// is JSON-encoded if non-nil.
func (m *Manager) runRaw(ctx context.Context, path, command string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, command)
	if stdin != nil {
		cmd.Stdin = bytesReader(stdin)
	}
	tee := newStderrTee(m.logger, filepath.Base(path), command)
	cmd.Stderr = tee

	stdout, err := cmd.Output()
	tee.flush()
	if err != nil {
		return nil, fmt.Errorf("plugin %s %s: %w (stderr: %s)",
			filepath.Base(path), command, err, tee.String())
	}
	return stdout, nil
}
