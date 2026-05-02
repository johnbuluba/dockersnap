package pluginsdk

import (
	"context"
	"fmt"
	"os/exec"
)

// FileFromString constructs a File entry from raw string content.
// Mode is taken as a Go octal literal (e.g. 0600) and serialized as
// the canonical four-digit string.
func FileFromString(name, content string, mode int) File {
	return File{
		Name:    name,
		Content: content,
		Mode:    fmt.Sprintf("%04o", mode),
	}
}

// NsenterCommand returns an exec.Cmd that runs the given command inside the
// given network namespace. Use it for sub-processes that must reach
// 127.0.0.1 bindings inside the netns (e.g. kubectl against kind's API
// server, curl against an in-namespace service).
//
// Plugin processes themselves run in the host netns; only specific
// sub-commands need namespace-internal execution.
func NsenterCommand(ctx context.Context, netnsName string, name string, args ...string) *exec.Cmd {
	full := append([]string{"--net=/run/netns/" + netnsName, "--", name}, args...)
	return exec.CommandContext(ctx, "nsenter", full...)
}

// DockerHostEnv returns a "DOCKER_HOST=unix://<socket>" env var suitable for
// passing to exec.Cmd.Env when invoking the docker CLI or kind.
func DockerHostEnv(socket string) string {
	return "DOCKER_HOST=unix://" + socket
}
