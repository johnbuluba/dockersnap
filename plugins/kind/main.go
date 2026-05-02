// dockersnap-plugin-kind deploys and manages kind (Kubernetes IN Docker)
// clusters inside a dockersnap instance. It implements the v1 plugin contract
// — see ../../docs/PLUGIN-DESIGN.md.
package main

import (
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
	"github.com/johnbuluba/dockersnap/pkg/version"
)

func main() {
	p := pluginsdk.New(pluginsdk.Plugin{
		Name:                      "kind",
		Version:                   version.String(),
		Description:               "Deploy and manage kind (Kubernetes IN Docker) clusters",
		SupportedContractVersions: []string{"1"},
		ConfigOptions: []pluginsdk.ConfigOption{
			{
				Name: "cluster_name", Type: pluginsdk.ConfigTypeString,
				Default:     "{{ instance_name }}",
				Description: "Name of the kind cluster. Defaults to the instance name.",
			},
			{
				Name: "kind_config", Type: pluginsdk.ConfigTypePath,
				Description: "Path to a kind cluster config YAML file.",
			},
			{
				Name: "wait_ready", Type: pluginsdk.ConfigTypeBool,
				Default:     true,
				Description: "Wait for all nodes to be Ready after cluster creation.",
			},
			{
				Name: "wait_timeout", Type: pluginsdk.ConfigTypeString,
				Default:     "120s",
				Description: "How long to wait for nodes to be Ready (Go duration string).",
			},
			{
				Name: "retries", Type: pluginsdk.ConfigTypeInt,
				Default:     3,
				Description: "Number of retries for cluster creation (handles flaky proxy).",
			},
			{
				Name: "kubernetes_version", Type: pluginsdk.ConfigTypeString,
				Description: "Kubernetes version to deploy (empty = kind default).",
			},
		},
	})

	p.OnInit(initHandler)
	p.OnValidate(validateHandler)
	p.OnDeploy(deployHandler)
	p.OnTeardown(teardownHandler)
	p.OnAccess(accessHandler)
	p.OnDescribe(describeHandler)
	p.OnHealth(healthHandler)

	p.Run()
}
