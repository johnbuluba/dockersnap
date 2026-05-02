package pluginsdk_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

func TestConfig_String(t *testing.T) {
	cfg := pluginsdk.NewConfigForTest(
		map[string]interface{}{"name": "demo"},
		[]pluginsdk.ConfigOption{
			{Name: "name", Type: pluginsdk.ConfigTypeString},
			{Name: "missing", Type: pluginsdk.ConfigTypeString, Default: "fallback"},
		},
	)
	assert.Equal(t, "demo", cfg.String("name"))
	assert.Equal(t, "fallback", cfg.String("missing"))
}

func TestConfig_String_PanicsOnUndeclared(t *testing.T) {
	cfg := pluginsdk.NewConfigForTest(map[string]interface{}{}, nil)
	assert.PanicsWithValue(t,
		`pluginsdk: config key "x" not declared in ConfigOptions`,
		func() { _ = cfg.String("x") })
}

func TestConfig_String_PanicsOnTypeMismatch(t *testing.T) {
	cfg := pluginsdk.NewConfigForTest(
		map[string]interface{}{"flag": true},
		[]pluginsdk.ConfigOption{{Name: "flag", Type: pluginsdk.ConfigTypeBool}},
	)
	assert.Panics(t, func() { _ = cfg.String("flag") })
}

func TestConfig_Bool(t *testing.T) {
	cfg := pluginsdk.NewConfigForTest(
		map[string]interface{}{"a": true},
		[]pluginsdk.ConfigOption{
			{Name: "a", Type: pluginsdk.ConfigTypeBool},
			{Name: "b", Type: pluginsdk.ConfigTypeBool, Default: true},
			{Name: "c", Type: pluginsdk.ConfigTypeBool},
		},
	)
	assert.True(t, cfg.Bool("a"))
	assert.True(t, cfg.Bool("b"))
	assert.False(t, cfg.Bool("c"))
}

func TestConfig_Int(t *testing.T) {
	cfg := pluginsdk.NewConfigForTest(
		map[string]interface{}{
			"a": float64(42), // JSON unmarshal produces float64
			"b": 7,
		},
		[]pluginsdk.ConfigOption{
			{Name: "a", Type: pluginsdk.ConfigTypeInt},
			{Name: "b", Type: pluginsdk.ConfigTypeInt},
			{Name: "c", Type: pluginsdk.ConfigTypeInt, Default: 3},
		},
	)
	assert.Equal(t, 42, cfg.Int("a"))
	assert.Equal(t, 7, cfg.Int("b"))
	assert.Equal(t, 3, cfg.Int("c"))
}

func TestConfig_Path(t *testing.T) {
	cfg := pluginsdk.NewConfigForTest(
		map[string]interface{}{"p": "/etc/kind.yaml"},
		[]pluginsdk.ConfigOption{{Name: "p", Type: pluginsdk.ConfigTypePath}},
	)
	assert.Equal(t, "/etc/kind.yaml", cfg.Path("p"))
}

func TestConfig_OptString(t *testing.T) {
	cfg := pluginsdk.NewConfigForTest(
		map[string]interface{}{"set": "yes"},
		[]pluginsdk.ConfigOption{
			{Name: "set", Type: pluginsdk.ConfigTypeString},
			{Name: "unset", Type: pluginsdk.ConfigTypeString},
			{Name: "default", Type: pluginsdk.ConfigTypeString, Default: "d"},
		},
	)

	v, ok := cfg.OptString("set")
	assert.Equal(t, "yes", v)
	assert.True(t, ok)

	v, ok = cfg.OptString("unset")
	assert.Equal(t, "", v)
	assert.False(t, ok)

	v, ok = cfg.OptString("default")
	assert.Equal(t, "d", v)
	assert.True(t, ok)
}

func TestConfig_StringList(t *testing.T) {
	cfg := pluginsdk.NewConfigForTest(
		map[string]interface{}{
			// Simulating both forms — JSON arrays come back as []interface{}.
			"raw":     []string{"a", "b"},
			"jsonish": []interface{}{"x", "y"},
		},
		[]pluginsdk.ConfigOption{
			{Name: "raw", Type: pluginsdk.ConfigTypeStringList},
			{Name: "jsonish", Type: pluginsdk.ConfigTypeStringList},
			{Name: "default", Type: pluginsdk.ConfigTypeStringList,
				Default: []string{"d1", "d2"}},
		},
	)
	assert.Equal(t, []string{"a", "b"}, cfg.StringList("raw"))
	assert.Equal(t, []string{"x", "y"}, cfg.StringList("jsonish"))
	assert.Equal(t, []string{"d1", "d2"}, cfg.StringList("default"))
}

func TestPortToken(t *testing.T) {
	assert.Equal(t, "${PORT:kubernetes-api}", pluginsdk.PortToken("kubernetes-api"))
	assert.Equal(t, "${PORT:ingress-http}", pluginsdk.PortToken("ingress-http"))
}

func TestForwardedPort_Lookup(t *testing.T) {
	c := &pluginsdk.Context{
		ForwardedPorts: []pluginsdk.ForwardedPort{
			{Label: "kubernetes-api", ContainerPort: 6443, HostPort: 34567, Protocol: "tcp"},
			{Label: "ingress-http", ContainerPort: 80, HostPort: 38080, Protocol: "tcp"},
		},
	}

	got, ok := c.ForwardedPort("kubernetes-api")
	assert.True(t, ok)
	assert.Equal(t, 34567, got.HostPort)

	_, ok = c.ForwardedPort("missing")
	assert.False(t, ok)
}

func TestFileFromString(t *testing.T) {
	f := pluginsdk.FileFromString("kubeconfig", "hello", 0600)
	assert.Equal(t, "kubeconfig", f.Name)
	assert.Equal(t, "hello", f.Content)
	assert.Equal(t, "0600", f.Mode)

	f = pluginsdk.FileFromString("ca", "x", 0644)
	assert.Equal(t, "0644", f.Mode)
}
