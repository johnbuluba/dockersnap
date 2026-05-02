package plugin

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// stderrTee is the stderr sink the runner attaches to a plugin process. Every
// line written by the plugin is:
//
//   - parsed as a pluginsdk.LogEntry NDJSON record. If it parses, the entry is
//     re-emitted into the daemon's slog at the matching level, with `plugin=`
//     / `instance=` / `command=` attrs preserved.
//   - logged verbatim at INFO if it doesn't parse — captures panics, plain
//     fmt.Println from a misbehaving plugin, etc.
//
// A bounded copy of the raw stream is also kept so error paths can still
// include "(stderr: …)" in their wrapped errors.
type stderrTee struct {
	logger     *slog.Logger
	pluginName string
	command    string
	rawCap     *cappedBuffer

	mu      sync.Mutex
	pending []byte
}

func newStderrTee(logger *slog.Logger, pluginName, command string) *stderrTee {
	return &stderrTee{
		logger:     logger,
		pluginName: pluginName,
		command:    command,
		rawCap:     newCappedBuffer(64 * 1024),
	}
}

// Write appends bytes to an internal line buffer; whenever a complete line
// arrives we hand it to processLine. This satisfies io.Writer so it slots in
// as cmd.Stderr.
func (t *stderrTee) Write(p []byte) (int, error) {
	_, _ = t.rawCap.Write(p)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.pending = append(t.pending, p...)
	for {
		idx := bytes.IndexByte(t.pending, '\n')
		if idx < 0 {
			break
		}
		line := t.pending[:idx]
		t.pending = t.pending[idx+1:]
		t.processLine(line)
	}
	return len(p), nil
}

// flush emits any trailing partial line that didn't end in \n. Called once
// the process has exited.
func (t *stderrTee) flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pending) > 0 {
		t.processLine(t.pending)
		t.pending = nil
	}
}

func (t *stderrTee) String() string { return t.rawCap.String() }

func (t *stderrTee) processLine(line []byte) {
	// Trim trailing \r for safety on cross-platform plugins.
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	if len(line) == 0 {
		return
	}

	// Try LogEntry first.
	if line[0] == '{' {
		var e pluginsdk.LogEntry
		if err := json.Unmarshal(line, &e); err == nil && e.Msg != "" {
			t.emitEntry(&e)
			return
		}
	}

	// Fallback: log verbatim. INFO so misbehaving plugins are visible
	// without flooding ERROR logs.
	t.logger.LogAttrs(nil, slog.LevelInfo, string(line),
		slog.String("plugin", t.pluginName),
		slog.String("command", t.command),
		slog.Bool("raw", true),
	)
}

func (t *stderrTee) emitEntry(e *pluginsdk.LogEntry) {
	level := parseLevel(e.Level)
	attrs := []slog.Attr{
		slog.String("plugin", firstNonEmpty(e.Plugin, t.pluginName)),
	}
	if cmd := firstNonEmpty(e.Command, t.command); cmd != "" {
		attrs = append(attrs, slog.String("command", cmd))
	}
	if e.Instance != "" {
		attrs = append(attrs, slog.String("instance", e.Instance))
	}
	for k, v := range e.Attrs {
		attrs = append(attrs, slog.Any(k, v))
	}
	t.logger.LogAttrs(nil, level, e.Msg, attrs...)
}

func parseLevel(s string) slog.Level {
	switch s {
	case "DEBUG", "debug":
		return slog.LevelDebug
	case "WARN", "warn", "WARNING", "warning":
		return slog.LevelWarn
	case "ERROR", "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

