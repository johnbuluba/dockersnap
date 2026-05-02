package sdktest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk/sdktest"
)

func TestNewContext_Defaults(t *testing.T) {
	in := sdktest.NewContext(t).Build()

	assert.Equal(t, "test", in.InstanceName)
	assert.Equal(t, "test", in.Instance.Name)
	assert.Equal(t, "/run/dockersnap/test.sock", in.Instance.Socket)
	assert.Equal(t, "ds-test", in.Instance.NetnsName)
	assert.Equal(t, "10.10.0.1", in.Instance.HostVethIP)
}

func TestNewContext_Overrides(t *testing.T) {
	in := sdktest.NewContext(t).
		WithInstanceName("demo").
		WithSubnet("10.20.0.0/16", "10.20.0.1", "10.20.0.2").
		WithEnv("HTTP_PROXY", "http://proxy").
		WithForwardedPort("kubernetes-api", 6443, 34567).
		WithConfig(
			map[string]interface{}{"name": "x"},
			[]pluginsdk.ConfigOption{{Name: "name", Type: pluginsdk.ConfigTypeString}},
		).
		Build()

	assert.Equal(t, "demo", in.Instance.Name)
	assert.Equal(t, "/run/dockersnap/demo.sock", in.Instance.Socket)
	assert.Equal(t, "ds-demo", in.Instance.NetnsName)
	assert.Equal(t, "10.20.0.0/16", in.Instance.Subnet)
	assert.Equal(t, "10.20.0.2", in.Instance.NsVethIP)
	assert.Equal(t, "http://proxy", in.Env["HTTP_PROXY"])

	got, ok := in.ForwardedPort("kubernetes-api")
	assert.True(t, ok)
	assert.Equal(t, 34567, got.HostPort)

	assert.Equal(t, "x", in.Config.String("name"))
}
