package main

import (
	"context"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// healthHandler reports the workload as healthy iff the container is in the
// "running" state. We don't attempt an HTTP probe — that would need the
// netns wrapper and isn't load-bearing for the test plugin.
func healthHandler(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.HealthResponse, error) {
	id, err := findContainerID(ctx, in)
	if err != nil {
		return &pluginsdk.HealthResponse{
			Healthy: false,
			Checks: []pluginsdk.HealthCheck{
				{Name: "container-lookup", OK: false, Message: err.Error()},
			},
		}, nil
	}
	if id == "" {
		return &pluginsdk.HealthResponse{
			Healthy: false,
			Checks: []pluginsdk.HealthCheck{
				{Name: "container-lookup", OK: false, Message: "no echo container found"},
			},
		}, nil
	}

	status := containerStatus(ctx, in, id)
	healthy := status == "running"
	return &pluginsdk.HealthResponse{
		Healthy: healthy,
		Checks: []pluginsdk.HealthCheck{
			{Name: "container-lookup", OK: true, Message: "id " + id[:12]},
			{Name: "container-state", OK: healthy, Message: status},
		},
	}, nil
}
