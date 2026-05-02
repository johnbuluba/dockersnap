package pluginsdk

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// LogStreamFD is the JSON field name used for log entries flowing from the
// plugin to the daemon over stderr. Each NDJSON line on stderr that decodes
// into a LogEntry is re-emitted into the daemon's slog at the matching level.
//
// The daemon's plugin runner parses every stderr line; lines that don't parse
// as a LogEntry get logged verbatim at INFO so panics and bare prints still
// surface.

// LogEntry is the on-the-wire shape for a single log event from a plugin.
// Fields map naturally to slog: Time → record time, Level → slog.Level
// (DEBUG/INFO/WARN/ERROR), Msg → message, Attrs → a flat map of attributes.
type LogEntry struct {
	Time     time.Time      `json:"ts"`
	Level    string         `json:"level"`
	Plugin   string         `json:"plugin"`
	Instance string         `json:"instance,omitempty"`
	Command  string         `json:"command,omitempty"`
	Msg      string         `json:"msg"`
	Attrs    map[string]any `json:"attrs,omitempty"`
}

// ndjsonLogHandler is the slog.Handler that plugins use. It serializes each
// record as a single-line LogEntry on os.Stderr, with `plugin` (and optionally
// `instance`/`command`) auto-injected from the bound attrs so plugin authors
// never have to remember to attach them.
//
// Stderr is the channel because:
//   - stdout is reserved for response JSON (validate/access/describe/health)
//     and progress NDJSON (deploy/teardown);
//   - stderr is line-oriented and already piped to the parent under systemd /
//     plain exec, so no extra plumbing needed.
type ndjsonLogHandler struct {
	w     io.Writer
	mu    *sync.Mutex
	level slog.Leveler

	// Bound attributes — plugin name baked in at construction, instance
	// added once the Runner has parsed PluginInput.
	plugin   string
	instance string
	command  string

	// Free-form attrs from WithAttrs / WithGroup (groups just prefix keys).
	attrs []slog.Attr
	group string
}

func (h *ndjsonLogHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	min := slog.LevelDebug
	if h.level != nil {
		min = h.level.Level()
	}
	return lvl >= min
}

func (h *ndjsonLogHandler) Handle(_ context.Context, r slog.Record) error {
	entry := LogEntry{
		Time:     r.Time,
		Level:    r.Level.String(),
		Plugin:   h.plugin,
		Instance: h.instance,
		Command:  h.command,
		Msg:      r.Message,
	}
	attrs := make(map[string]any, r.NumAttrs()+len(h.attrs))
	for _, a := range h.attrs {
		putAttr(attrs, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		putAttr(attrs, h.group, a)
		return true
	})
	if len(attrs) > 0 {
		entry.Attrs = attrs
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	}

	buf, err := json.Marshal(&entry)
	if err != nil {
		return err
	}
	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err = h.w.Write(buf)
	return err
}

func (h *ndjsonLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

func (h *ndjsonLogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	if h.group != "" {
		clone.group = h.group + "." + name
	} else {
		clone.group = name
	}
	return &clone
}

func putAttr(m map[string]any, group string, a slog.Attr) {
	if a.Key == "" {
		return
	}
	key := a.Key
	if group != "" {
		key = group + "." + key
	}
	m[key] = a.Value.Resolve().Any()
}

// NewLogger returns a *slog.Logger configured to emit NDJSON LogEntry lines
// to os.Stderr, with the plugin's name auto-attached. Plugin authors call
// this once in main() (or rely on the Runner to construct one for them via
// Context.Logger) and use it like any other slog.Logger.
//
// The DOCKERSNAP_LOG_LEVEL env var, if set to debug/info/warn/error, controls
// the minimum level. Default is debug — the daemon does the filtering.
func NewLogger(p Plugin) *slog.Logger {
	return slog.New(newPluginHandler(os.Stderr, p.Name, levelFromEnv()))
}

func newPluginHandler(w io.Writer, pluginName string, level slog.Leveler) *ndjsonLogHandler {
	return &ndjsonLogHandler{
		w:      w,
		mu:     &sync.Mutex{},
		level:  level,
		plugin: pluginName,
	}
}

func levelFromEnv() slog.Level {
	switch os.Getenv("DOCKERSNAP_LOG_LEVEL") {
	case "error", "ERROR":
		return slog.LevelError
	case "warn", "WARN":
		return slog.LevelWarn
	case "info", "INFO":
		return slog.LevelInfo
	default:
		return slog.LevelDebug
	}
}

// withInstance returns a clone of h with the instance/command bound. Used by
// the Runner once it has parsed PluginInput so handler-side loggers carry
// `instance` automatically.
func (h *ndjsonLogHandler) withInstance(instance, command string) *ndjsonLogHandler {
	clone := *h
	clone.instance = instance
	clone.command = command
	return &clone
}

// NewLogHandlerForTest constructs the NDJSON slog handler with an explicit
// io.Writer, plugin name, instance, command, and level. Test-only: production
// code should call NewLogger.
func NewLogHandlerForTest(w io.Writer, pluginName, instance, command string, level slog.Leveler) slog.Handler {
	h := newPluginHandler(w, pluginName, level)
	h.instance = instance
	h.command = command
	return h
}
