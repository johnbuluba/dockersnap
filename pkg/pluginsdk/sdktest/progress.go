package sdktest

import (
	"bufio"
	"bytes"
	"encoding/json"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// RecordingProgress captures progress events for assertion in tests.
//
// Usage:
//
//	rp := sdktest.NewProgress()
//	err := deployHandler(t.Context(), in, rp.Progress())
//	require.NoError(t, err)
//	assert.Contains(t, rp.Steps(), "creating_cluster")
type RecordingProgress struct {
	buf *bytes.Buffer
	p   *pluginsdk.Progress
}

// NewProgress returns a RecordingProgress whose Progress() can be passed to a
// handler.
func NewProgress() *RecordingProgress {
	buf := &bytes.Buffer{}
	return &RecordingProgress{buf: buf, p: pluginsdk.NewProgress(buf)}
}

// Progress returns the *pluginsdk.Progress to pass into a handler.
func (r *RecordingProgress) Progress() *pluginsdk.Progress { return r.p }

// Events returns every emitted event in order.
func (r *RecordingProgress) Events() []pluginsdk.ProgressEvent {
	var out []pluginsdk.ProgressEvent
	scanner := bufio.NewScanner(bytes.NewReader(r.buf.Bytes()))
	for scanner.Scan() {
		var ev pluginsdk.ProgressEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
			out = append(out, ev)
		}
	}
	return out
}

// Steps returns just the step names, in emission order, with duplicates kept
// (so tests can assert on transitions like running -> done).
func (r *RecordingProgress) Steps() []string {
	events := r.Events()
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Step)
	}
	return out
}

// HasError returns true if any emitted event has Status == "error".
func (r *RecordingProgress) HasError() bool {
	for _, e := range r.Events() {
		if e.Status == "error" {
			return true
		}
	}
	return false
}

// Raw returns the raw NDJSON output as bytes (one event per line).
func (r *RecordingProgress) Raw() []byte { return r.buf.Bytes() }
