package main

import (
	"context"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// describeHandler returns workload metadata + the container's runtime status.
func describeHandler(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.DescribeResponse, error) {
	port := in.Config.Int("port")

	resp := &pluginsdk.DescribeResponse{
		WorkloadType: "echo",
		Status:       "missing",
		Ports: []pluginsdk.PortInfo{
			{Label: "echo", ContainerPort: port, Protocol: "tcp"},
		},
		Config: map[string]interface{}{
			"image": in.Config.String("image"),
			"text":  in.Config.String("text"),
			"port":  port,
		},
	}

	id, err := findContainerID(ctx, in)
	if err == nil && id != "" {
		resp.Status = containerStatus(ctx, in, id)
		resp.Details = map[string]interface{}{
			"container_id": id,
		}
	}

	return resp, nil
}
