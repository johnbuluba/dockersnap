package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetCreateFlags wipes the package-level flag state between subtests; the
// flags are accumulated globals so tests have to clean up after themselves.
func resetCreateFlags() {
	createPlugin = ""
	createConfigKVs = nil
	createConfigFile = ""
}

func TestBuildInlineWorkload_NoFlags(t *testing.T) {
	resetCreateFlags()
	inline, err := buildInlineWorkload()
	require.NoError(t, err)
	assert.Nil(t, inline, "no inline flags → no inline workload")
}

func TestBuildInlineWorkload_PluginRequired(t *testing.T) {
	resetCreateFlags()
	createConfigKVs = []string{"foo=bar"}
	_, err := buildInlineWorkload()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--plugin is required")
}

func TestBuildInlineWorkload_KVOnly(t *testing.T) {
	resetCreateFlags()
	createPlugin = "kind"
	createConfigKVs = []string{
		"cluster_name=foo",
		"retries=5",
		"wait_ready=true",
		"port=8443",
	}
	inline, err := buildInlineWorkload()
	require.NoError(t, err)
	require.NotNil(t, inline)
	assert.Equal(t, "kind", inline.Plugin)
	assert.Equal(t, "foo", inline.Config["cluster_name"])
	assert.EqualValues(t, 5, inline.Config["retries"])
	assert.Equal(t, true, inline.Config["wait_ready"])
	assert.EqualValues(t, 8443, inline.Config["port"])
}

func TestBuildInlineWorkload_FileMergedWithKV(t *testing.T) {
	resetCreateFlags()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "kind.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte(`
cluster_name: from-file
retries: 1
wait_ready: false
`), 0o600))

	createPlugin = "kind"
	createConfigFile = cfg
	createConfigKVs = []string{"retries=9"} // overrides file value

	inline, err := buildInlineWorkload()
	require.NoError(t, err)
	require.NotNil(t, inline)
	assert.Equal(t, "from-file", inline.Config["cluster_name"], "file value passes through")
	assert.EqualValues(t, 9, inline.Config["retries"], "--config flag overrides file")
	assert.Equal(t, false, inline.Config["wait_ready"])
}

func TestBuildInlineWorkload_BadKV(t *testing.T) {
	resetCreateFlags()
	createPlugin = "kind"
	createConfigKVs = []string{"missing-equals"}
	_, err := buildInlineWorkload()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected key=value")
}

func TestParseConfigValue(t *testing.T) {
	cases := []struct {
		in   string
		want interface{}
	}{
		{"true", true},
		{"false", false},
		{"null", nil},
		{"42", int64(42)},
		{"3.14", 3.14},
		{`["a","b"]`, []interface{}{"a", "b"}},
		{`{"k":"v"}`, map[string]interface{}{"k": "v"}},
		{"plain string", "plain string"},
		{"", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			assert.Equal(t, c.want, parseConfigValue(c.in))
		})
	}
}
