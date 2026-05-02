package pluginsdk_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk/sdktest"
)

// decodeLines splits NDJSON output into LogEntry records, ignoring empty
// lines so trailing newlines don't trip the test.
func decodeLines(t *testing.T, b []byte) []pluginsdk.LogEntry {
	t.Helper()
	var out []pluginsdk.LogEntry
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e pluginsdk.LogEntry
		require.NoError(t, json.Unmarshal(line, &e),
			"line: %s", string(line))
		out = append(out, e)
	}
	return out
}

func TestLogger_NDJSONFormatAndAttrs(t *testing.T) {
	// Drive a real handler with a buffer in place of stderr so we can read
	// what would have been written.
	var buf bytes.Buffer
	logger := slog.New(sdktest.NewLogHandler(&buf, "kind", "inst-7", "deploy"))

	logger.Info("creating cluster", "cluster", "kind", "retries", 3)
	logger.Warn("retrying", "attempt", 2)
	logger.Error("gave up")

	entries := decodeLines(t, buf.Bytes())
	require.Len(t, entries, 3)

	assert.Equal(t, "INFO", entries[0].Level)
	assert.Equal(t, "kind", entries[0].Plugin)
	assert.Equal(t, "inst-7", entries[0].Instance)
	assert.Equal(t, "deploy", entries[0].Command)
	assert.Equal(t, "creating cluster", entries[0].Msg)
	assert.Equal(t, "kind", entries[0].Attrs["cluster"])
	assert.EqualValues(t, 3, entries[0].Attrs["retries"])
	assert.False(t, entries[0].Time.IsZero())

	assert.Equal(t, "WARN", entries[1].Level)
	assert.EqualValues(t, 2, entries[1].Attrs["attempt"])

	assert.Equal(t, "ERROR", entries[2].Level)
}

func TestLogger_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(sdktest.NewLogHandlerLevel(&buf, "kind", slog.LevelInfo))

	logger.Debug("noisy") // dropped
	logger.Info("kept")

	entries := decodeLines(t, buf.Bytes())
	require.Len(t, entries, 1)
	assert.Equal(t, "kept", entries[0].Msg)
}

func TestLogger_GroupAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(sdktest.NewLogHandler(&buf, "kind", "inst-1", "health"))

	// WithGroup should prefix attribute keys.
	logger.WithGroup("kube").Info("probe", "endpoint", "/healthz")

	entries := decodeLines(t, buf.Bytes())
	require.Len(t, entries, 1)
	assert.Equal(t, "/healthz", entries[0].Attrs["kube.endpoint"])
}

func TestLogger_LineIsValidNDJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(sdktest.NewLogHandler(&buf, "kind", "", ""))
	logger.Info("hi", "x", 1)

	// Exactly one record; ends with exactly one newline; the record contains
	// no embedded newlines.
	output := buf.String()
	assert.True(t, strings.HasSuffix(output, "\n"))
	body := strings.TrimRight(output, "\n")
	assert.NotContains(t, body, "\n",
		"record bodies must not contain embedded newlines (would break NDJSON parsing)")
}
