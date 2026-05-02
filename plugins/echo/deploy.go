package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// deployHandler runs the echo container if it isn't already running.
// Idempotent: if a container with our labels already exists, we leave
// it alone and emit a "complete" event.
func deployHandler(ctx context.Context, in *pluginsdk.Context, p *pluginsdk.Progress) error {
	image := in.Config.String("image")
	text := in.Config.String("text")
	port := in.Config.Int("port")

	p.Step("checking_existing", "Checking for existing echo container")
	if id, err := findContainerID(ctx, in); err == nil && id != "" {
		p.Done("checking_existing")
		p.Complete("echo container %s already running", id[:12])
		return nil
	}
	p.Done("checking_existing")

	p.Step("pulling_image", fmt.Sprintf("Pulling %s", image))
	pullCmd := dockerCmd(ctx, in, "pull", image)
	if out, err := pullCmd.CombinedOutput(); err != nil {
		// Non-fatal: image may already be cached locally and pull may fail
		// behind a strict proxy. We'll let `docker run` surface the real
		// error if the image is genuinely missing.
		p.Step("pulling_image", fmt.Sprintf("pull failed (%s); continuing", trimFirstLine(out)))
	}
	p.Done("pulling_image")

	portStr := strconv.Itoa(port)
	p.Step("running_container", fmt.Sprintf("Starting echo on container port %d", port))
	runCmd := dockerCmd(ctx, in,
		"run", "--detach",
		// `unless-stopped` so the container comes back automatically after
		// `dockersnap stop` + `start` (dockerd dies, dockerd comes back —
		// without this the container stays Exited and the workload is dark).
		// `kind` containers set their own restart policy; this is the same
		// idea applied to the echo reference plugin.
		"--restart", "unless-stopped",
		"--label", labelPlugin+"=echo",
		"--label", labelInstance+"="+in.InstanceName,
		"--publish", "0:"+portStr,
		image,
		"-listen=:"+portStr,
		"-text="+text,
	)
	if out, err := runCmd.CombinedOutput(); err != nil {
		return p.Fail("running_container", errCombined(err, out))
	}
	p.Done("running_container")

	p.Complete("echo container running, listening on container port %d", port)
	return nil
}

func trimFirstLine(out []byte) string {
	for i, b := range out {
		if b == '\n' {
			return string(out[:i])
		}
	}
	return string(out)
}
