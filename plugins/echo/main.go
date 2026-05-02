// dockersnap-plugin-echo runs a single HTTP echo server inside a dockersnap
// instance. It's the smallest workload that exercises every command in the
// v1 contract — deploy/teardown spawn and remove a real container, access
// produces an env + endpoint, health probes the container — without needing
// anything heavier than the docker CLI.
//
// Useful as:
//   - a smoke test for the plugin system end-to-end
//   - a "hello world" plugin authors can read in one sitting
//   - a stand-in workload when you want a dockersnap instance with *some*
//     visible service but don't want to wait for kind
package main

import (
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
	"github.com/johnbuluba/dockersnap/pkg/version"
)

const (
	// labelPlugin / labelInstance are stamped on every container we create
	// so teardown/describe/health can find them without persisting state.
	labelPlugin   = "dockersnap.plugin"
	labelInstance = "dockersnap.instance"
)

func main() {
	p := pluginsdk.New(pluginsdk.Plugin{
		Name:                      "echo",
		Version:                   version.String(),
		Description:               "Run a simple HTTP echo container — useful for testing the plugin system",
		SupportedContractVersions: []string{"1"},
		ConfigOptions: []pluginsdk.ConfigOption{
			{
				Name: "image", Type: pluginsdk.ConfigTypeString,
				Default:     "hashicorp/http-echo:latest",
				Description: "Container image to run.",
			},
			{
				Name: "text", Type: pluginsdk.ConfigTypeString,
				Default:     "hello from {{ instance_name }}",
				Description: "Text the echo server returns on every request.",
			},
			{
				Name: "port", Type: pluginsdk.ConfigTypeInt,
				Default:     5678,
				Description: "Container port the echo server listens on.",
			},
		},
	})

	p.OnInit(initHandler)
	p.OnDeploy(deployHandler)
	p.OnTeardown(teardownHandler)
	p.OnAccess(accessHandler)
	p.OnDescribe(describeHandler)
	p.OnHealth(healthHandler)

	p.Run()
}
