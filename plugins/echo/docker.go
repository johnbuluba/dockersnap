package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// dockerCmd builds an exec.Cmd targeting the instance's docker socket.
func dockerCmd(ctx context.Context, in *pluginsdk.Context, args ...string) *exec.Cmd {
	full := append([]string{"-H", "unix://" + in.Instance.Socket}, args...)
	return exec.CommandContext(ctx, "docker", full...)
}

// findContainerID returns the running container's ID for this instance, or
// "" if none is running. We filter only by the plugin label because the
// docker socket we're talking to (`in.Instance.Socket`) is already
// per-dockersnap-instance — there is no other instance's container we
// could see. Filtering by labelInstance as well looks defensive but
// breaks under clone: the clone's container carries the source's
// labelInstance value (baked in at `docker run` time and copied verbatim
// by ZFS clone), so the instance-name match would always miss.
func findContainerID(ctx context.Context, in *pluginsdk.Context) (string, error) {
	cmd := dockerCmd(ctx, in,
		"ps", "--quiet",
		"--filter", "label="+labelPlugin+"=echo",
	)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))
	if strings.Contains(id, "\n") {
		// Multiple matches — return the first; teardown will sweep them all.
		id = strings.SplitN(id, "\n", 2)[0]
	}
	return id, nil
}

// findAllContainerIDs returns every container (running or otherwise)
// the echo plugin owns on this instance's docker socket. Same
// label-filter rationale as findContainerID.
func findAllContainerIDs(ctx context.Context, in *pluginsdk.Context) ([]string, error) {
	cmd := dockerCmd(ctx, in,
		"ps", "--all", "--quiet",
		"--filter", "label="+labelPlugin+"=echo",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(strings.TrimSpace(string(out))), nil
}

// inspectField runs `docker inspect --format <fmt> <id>` and returns the
// trimmed output. Returns "" on any error.
func inspectField(ctx context.Context, in *pluginsdk.Context, id, format string) string {
	cmd := dockerCmd(ctx, in, "inspect", "--format", format, id)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// containerStatus returns the .State.Status field for a container, e.g.
// "running", "exited", "created". Returns "" if the container is gone.
func containerStatus(ctx context.Context, in *pluginsdk.Context, id string) string {
	return inspectField(ctx, in, id, "{{.State.Status}}")
}

// errCombined produces a one-line error message from a (err, output) pair.
func errCombined(err error, out []byte) error {
	return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
}
