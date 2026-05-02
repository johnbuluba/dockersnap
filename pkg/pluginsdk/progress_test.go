package pluginsdk_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk/sdktest"
)

func TestProgress_StepDoneComplete(t *testing.T) {
	rp := sdktest.NewProgress()
	p := rp.Progress()

	p.Step("creating", "Creating cluster")
	p.Done("creating")
	p.Complete("cluster %q created", "test")

	events := rp.Events()
	assert.Equal(t, []pluginsdk.ProgressEvent{
		{Step: "creating", Status: "running", Message: "Creating cluster"},
		{Step: "creating", Status: "done"},
		{Step: "complete", Status: "done", Message: `cluster "test" created`},
	}, events)
}

func TestProgress_Fail_ReturnsErrAndEmits(t *testing.T) {
	rp := sdktest.NewProgress()
	myErr := errors.New("kind not installed")

	got := rp.Progress().Fail("checking_prereqs", myErr)

	assert.Same(t, myErr, got, "Fail should return the same error unchanged")
	assert.True(t, rp.HasError())
	events := rp.Events()
	assert.Equal(t, "error", events[0].Status)
	assert.Equal(t, "kind not installed", events[0].Message)
}

func TestProgress_NilReceiverIsSafe(t *testing.T) {
	var p *pluginsdk.Progress // nil

	assert.NotPanics(t, func() {
		p.Step("x", "y")
		p.Done("x")
		_ = p.Fail("x", errors.New("err"))
		p.Complete("done")
	})
}

func TestProgress_Steps(t *testing.T) {
	rp := sdktest.NewProgress()
	p := rp.Progress()

	p.Step("a", "")
	p.Done("a")
	p.Step("b", "")
	p.Fail("b", errors.New("bad"))

	assert.Equal(t, []string{"a", "a", "b", "b"}, rp.Steps())
}

func TestProgress_NewProgress_NilWriter(t *testing.T) {
	// Passing nil writer falls back to os.Stdout — just verify no panic.
	p := pluginsdk.NewProgress(nil)
	assert.NotPanics(t, func() { p.Step("x", "") })
}

func TestProgress_RawNDJSON(t *testing.T) {
	buf := &bytes.Buffer{}
	p := pluginsdk.NewProgress(buf)
	p.Step("x", "starting")
	p.Done("x")

	// Expect two lines, each one valid JSON.
	got := buf.String()
	assert.Contains(t, got, `"step":"x"`)
	assert.Contains(t, got, `"status":"running"`)
	assert.Contains(t, got, `"status":"done"`)
}
