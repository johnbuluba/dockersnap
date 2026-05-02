package plugin

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLogs returns a slog.Logger that writes JSON records to buf and a
// helper that decodes them as a slice of maps.
func captureLogs(t *testing.T) (*slog.Logger, *bytes.Buffer, func() []map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	decode := func() []map[string]any {
		var out []map[string]any
		for _, line := range bytes.Split(buf.Bytes(), []byte{'\n'}) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var m map[string]any
			require.NoError(t, json.Unmarshal(line, &m), "line: %s", string(line))
			out = append(out, m)
		}
		return out
	}
	return logger, &buf, decode
}

func TestStderrTee_ReemitsStructuredLogEntries(t *testing.T) {
	logger, _, decode := captureLogs(t)
	tee := newStderrTee(logger, "kind", "deploy")

	// One full line + a partial line that completes only after flush.
	_, err := tee.Write([]byte(`{"ts":"2026-05-02T00:00:00Z","level":"WARN","plugin":"kind","instance":"foo","msg":"retrying","attrs":{"attempt":2}}` + "\n"))
	require.NoError(t, err)
	tee.flush()

	records := decode()
	require.Len(t, records, 1)
	assert.Equal(t, "WARN", records[0]["level"])
	assert.Equal(t, "retrying", records[0]["msg"])
	assert.Equal(t, "kind", records[0]["plugin"])
	assert.Equal(t, "foo", records[0]["instance"])
	assert.Equal(t, "deploy", records[0]["command"])
	assert.EqualValues(t, 2, records[0]["attempt"])
}

func TestStderrTee_FallsBackToInfoForNonJSONLines(t *testing.T) {
	logger, _, decode := captureLogs(t)
	tee := newStderrTee(logger, "kind", "deploy")

	_, err := tee.Write([]byte("panic: runtime error\n\tgoroutine 1\n"))
	require.NoError(t, err)
	tee.flush()

	records := decode()
	require.Len(t, records, 2, "two non-JSON lines should produce two INFO records")
	for _, r := range records {
		assert.Equal(t, "INFO", r["level"])
		assert.Equal(t, "kind", r["plugin"])
		assert.Equal(t, "deploy", r["command"])
		assert.Equal(t, true, r["raw"])
	}
	assert.Equal(t, "panic: runtime error", records[0]["msg"])
}

func TestStderrTee_HandlesPartialWrites(t *testing.T) {
	logger, _, decode := captureLogs(t)
	tee := newStderrTee(logger, "kind", "health")

	chunks := []string{
		`{"ts":"2026-05-02T00:00:00Z","level":"INFO","plugin":"kind","msg":"hello`,
		`","attrs":{"k":"v"}}` + "\n",
	}
	for _, c := range chunks {
		_, err := tee.Write([]byte(c))
		require.NoError(t, err)
	}
	tee.flush()

	records := decode()
	require.Len(t, records, 1)
	assert.Equal(t, "hello", records[0]["msg"])
	assert.Equal(t, "v", records[0]["k"])
}

func TestStderrTee_RetainsRawForErrorContext(t *testing.T) {
	logger, _, _ := captureLogs(t)
	tee := newStderrTee(logger, "kind", "deploy")

	_, err := tee.Write([]byte("plain output\n"))
	require.NoError(t, err)
	tee.flush()

	assert.True(t, strings.Contains(tee.String(), "plain output"),
		"raw stderr must remain available for error wrapping; got %q", tee.String())
}
