package pluginsdk

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Progress emits NDJSON ProgressEvent lines to its writer (stdout by default
// when the plugin is run as a subprocess). It is safe to call methods on a
// nil receiver — they no-op so handlers can run inside unit tests without
// installing a fake writer.
type Progress struct {
	out io.Writer
}

// NewProgress returns a Progress that writes events to w. Pass os.Stdout in
// production; pass a bytes.Buffer in tests.
func NewProgress(w io.Writer) *Progress {
	if w == nil {
		w = os.Stdout
	}
	return &Progress{out: w}
}

// Step emits a "running" event for the named step.
func (p *Progress) Step(step, message string) {
	if p == nil {
		return
	}
	p.emit(ProgressEvent{Step: step, Status: "running", Message: message})
}

// Done emits a "done" event for the named step.
func (p *Progress) Done(step string) {
	if p == nil {
		return
	}
	p.emit(ProgressEvent{Step: step, Status: "done"})
}

// Fail emits an "error" event for the named step and returns the error
// unchanged so handlers can `return p.Fail(...)` in one line.
func (p *Progress) Fail(step string, err error) error {
	if p == nil {
		return err
	}
	p.emit(ProgressEvent{Step: step, Status: "error", Message: err.Error()})
	return err
}

// Complete emits the terminal "complete" event with a formatted message.
// The SDK's Run() emits this automatically before exit if the handler
// hasn't, so calling Complete is optional.
func (p *Progress) Complete(format string, args ...interface{}) {
	if p == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	p.emit(ProgressEvent{Step: "complete", Status: "done", Message: msg})
}

func (p *Progress) emit(e ProgressEvent) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	fmt.Fprintln(p.out, string(data))
	if f, ok := p.out.(interface{ Sync() error }); ok {
		_ = f.Sync()
	}
	if f, ok := p.out.(interface{ Flush() error }); ok {
		_ = f.Flush()
	}
}
