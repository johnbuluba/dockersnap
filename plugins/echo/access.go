package main

import (
	"context"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// accessHandler returns (when the proxy has surfaced our container's
// port) an ECHO_URL env var + endpoint pointing at the echo service.
// The daemon resolves ${HOST} and ${PORT:<label>} before sending the
// response back; the CLI resolves ${ACCESS_DIR}. DOCKER_HOST and
// DOCKERSNAP_INSTANCE are injected by the daemon — plugins shouldn't
// emit them.
func accessHandler(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.AccessResponse, error) {
	configuredPort := in.Config.Int("port")

	resp := &pluginsdk.AccessResponse{
		Env: map[string]string{},
	}

	// Find the proxy entry for our container port. The proxy assigns its own
	// label per published port (e.g. "container:abc123/5678/tcp"), and we
	// don't know that label until the proxy has scanned our running container.
	// If access is called before the proxy has caught up, we just return env
	// without an endpoint — the caller can re-call after a few seconds.
	for _, fp := range in.ForwardedPorts {
		if fp.ContainerPort == configuredPort {
			resp.Env["ECHO_URL"] = "http://" + pluginsdk.HostToken + ":" + pluginsdk.PortToken(fp.Label)
			resp.Endpoints = []pluginsdk.Endpoint{
				{
					Name:          "echo",
					Scheme:        "http",
					HostPortLabel: fp.Label,
					Description:   "HTTP echo endpoint",
				},
			}
			break
		}
	}

	return resp, nil
}
