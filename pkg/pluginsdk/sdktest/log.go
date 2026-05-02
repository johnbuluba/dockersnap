package sdktest

import (
	"io"
	"log/slog"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// NewLogHandler returns a slog.Handler that emits NDJSON LogEntry lines to w.
// Bound `instance` and `command` are optional — pass empty strings to skip.
//
// This is the test entry point for exercising the plugin SDK's log handler
// without spinning up a Runner. Production code uses pluginsdk.NewLogger.
func NewLogHandler(w io.Writer, pluginName, instance, command string) slog.Handler {
	return pluginsdk.NewLogHandlerForTest(w, pluginName, instance, command, slog.LevelDebug)
}

// NewLogHandlerLevel is NewLogHandler with an explicit minimum level.
func NewLogHandlerLevel(w io.Writer, pluginName string, level slog.Leveler) slog.Handler {
	return pluginsdk.NewLogHandlerForTest(w, pluginName, "", "", level)
}
