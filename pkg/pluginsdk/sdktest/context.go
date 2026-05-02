package sdktest

import (
	"testing"
	"time"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// ContextBuilder builds a fully-populated *pluginsdk.Context for tests.
// All fields default to plausible values; chain With* methods to override.
type ContextBuilder struct {
	t              testing.TB
	instanceName   string
	instance       pluginsdk.Instance
	forwardedPorts []pluginsdk.ForwardedPort
	env            map[string]string
	configRaw      map[string]interface{}
	configOptions  []pluginsdk.ConfigOption
}

// NewContext returns a ContextBuilder pre-populated with test defaults.
func NewContext(t testing.TB) *ContextBuilder {
	t.Helper()
	name := "test"
	return &ContextBuilder{
		t:            t,
		instanceName: name,
		instance: pluginsdk.Instance{
			Name:       name,
			Socket:     "/run/dockersnap/" + name + ".sock",
			Subnet:     "10.10.0.0/16",
			DataRoot:   "/dockersnap/instances/" + name,
			HostVethIP: "10.10.0.1",
			NsVethIP:   "10.10.0.2",
			NetnsName:  "ds-" + name,
			MetalLBIP:  "10.10.10.10",
			CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		env:       map[string]string{},
		configRaw: map[string]interface{}{},
	}
}

// WithInstanceName sets the instance name and updates derived fields
// (socket, data_root, netns_name) to match.
func (b *ContextBuilder) WithInstanceName(name string) *ContextBuilder {
	b.instanceName = name
	b.instance.Name = name
	b.instance.Socket = "/run/dockersnap/" + name + ".sock"
	b.instance.DataRoot = "/dockersnap/instances/" + name
	b.instance.NetnsName = "ds-" + name
	return b
}

// WithSubnet sets the subnet and recomputes host/ns veth IPs.
func (b *ContextBuilder) WithSubnet(subnet string, hostIP, nsIP string) *ContextBuilder {
	b.instance.Subnet = subnet
	b.instance.HostVethIP = hostIP
	b.instance.NsVethIP = nsIP
	return b
}

// WithForwardedPort appends one entry to the forwarded_ports list.
func (b *ContextBuilder) WithForwardedPort(label string, containerPort, hostPort int) *ContextBuilder {
	b.forwardedPorts = append(b.forwardedPorts, pluginsdk.ForwardedPort{
		Label:         label,
		ContainerPort: containerPort,
		HostPort:      hostPort,
		Protocol:      "tcp",
	})
	return b
}

// WithEnv sets one entry in the env map.
func (b *ContextBuilder) WithEnv(key, value string) *ContextBuilder {
	b.env[key] = value
	return b
}

// WithConfig populates plugin_config + the schema declaring its options.
// The Config accessor will only allow keys present in opts.
func (b *ContextBuilder) WithConfig(raw map[string]interface{}, opts []pluginsdk.ConfigOption) *ContextBuilder {
	b.configRaw = raw
	b.configOptions = opts
	return b
}

// Build returns the populated *pluginsdk.Context.
func (b *ContextBuilder) Build() *pluginsdk.Context {
	b.t.Helper()
	return &pluginsdk.Context{
		InstanceName:   b.instanceName,
		Instance:       b.instance,
		ForwardedPorts: b.forwardedPorts,
		Env:            b.env,
		Config:         pluginsdk.NewConfigForTest(b.configRaw, b.configOptions),
	}
}
