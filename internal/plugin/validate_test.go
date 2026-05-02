package plugin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

func TestValidateConfig_ValidCases(t *testing.T) {
	opts := []pluginsdk.ConfigOption{
		{Name: "cluster_name", Type: pluginsdk.ConfigTypeString},
		{Name: "wait_ready", Type: pluginsdk.ConfigTypeBool},
		{Name: "retries", Type: pluginsdk.ConfigTypeInt},
	}

	issues := ValidateConfig(opts, map[string]interface{}{
		"cluster_name": "demo",
		"wait_ready":   true,
		"retries":      float64(3), // JSON numbers are float64
	})
	assert.Empty(t, issues)
}

func TestValidateConfig_RejectsUnknownKey(t *testing.T) {
	opts := []pluginsdk.ConfigOption{
		{Name: "cluster_name", Type: pluginsdk.ConfigTypeString}, // not required
	}
	issues := ValidateConfig(opts, map[string]interface{}{
		"cluster_namee": "typo", // typo
	})
	require.Len(t, issues, 1)
	assert.Equal(t, `unknown config key "cluster_namee"`, issues[0])
}

func TestValidateConfig_TypeMismatch(t *testing.T) {
	opts := []pluginsdk.ConfigOption{
		{Name: "wait_ready", Type: pluginsdk.ConfigTypeBool},
		{Name: "retries", Type: pluginsdk.ConfigTypeInt},
	}
	issues := ValidateConfig(opts, map[string]interface{}{
		"wait_ready": "yes please", // wrong type
		"retries":    "three",      // wrong type
	})
	require.Len(t, issues, 2)
}

func TestValidateConfig_RequiredMissing(t *testing.T) {
	opts := []pluginsdk.ConfigOption{
		{Name: "cluster_name", Type: pluginsdk.ConfigTypeString, Required: true},
	}
	issues := ValidateConfig(opts, map[string]interface{}{})
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "missing required")
}

func TestValidateConfig_PathMustExist(t *testing.T) {
	tmp := t.TempDir()
	existing := filepath.Join(tmp, "kind.yaml")
	require.NoError(t, os.WriteFile(existing, []byte("kind: Cluster"), 0o644))

	opts := []pluginsdk.ConfigOption{
		{Name: "kind_config", Type: pluginsdk.ConfigTypePath},
	}

	t.Run("existing path passes", func(t *testing.T) {
		assert.Empty(t, ValidateConfig(opts, map[string]interface{}{"kind_config": existing}))
	})

	t.Run("missing path fails", func(t *testing.T) {
		issues := ValidateConfig(opts, map[string]interface{}{
			"kind_config": filepath.Join(tmp, "missing.yaml"),
		})
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0], "does not exist")
	})
}

func TestValidateConfig_StringList(t *testing.T) {
	opts := []pluginsdk.ConfigOption{
		{Name: "tags", Type: pluginsdk.ConfigTypeStringList},
	}

	t.Run("native []string", func(t *testing.T) {
		assert.Empty(t, ValidateConfig(opts, map[string]interface{}{
			"tags": []string{"a", "b"},
		}))
	})

	t.Run("[]interface{} of strings", func(t *testing.T) {
		assert.Empty(t, ValidateConfig(opts, map[string]interface{}{
			"tags": []interface{}{"a", "b"},
		}))
	})

	t.Run("[]interface{} with non-string", func(t *testing.T) {
		issues := ValidateConfig(opts, map[string]interface{}{
			"tags": []interface{}{"a", 7},
		})
		require.Len(t, issues, 1)
	})
}

func TestResolveConfig_InstanceName(t *testing.T) {
	in := map[string]interface{}{
		"cluster_name": "{{ instance_name }}",
		"prefix":       "ds-{{ instance_name }}-x",
		"untouched":    7,
	}
	out := ResolveConfig(in, "demo")
	assert.Equal(t, "demo", out["cluster_name"])
	assert.Equal(t, "ds-demo-x", out["prefix"])
	assert.Equal(t, 7, out["untouched"])
}

func TestResolveConfig_NestedMap(t *testing.T) {
	in := map[string]interface{}{
		"proxy": map[string]interface{}{
			"http":  "http://proxy/{{ instance_name }}",
			"https": "http://proxy",
		},
	}
	out := ResolveConfig(in, "demo")
	nested := out["proxy"].(map[string]interface{})
	assert.Equal(t, "http://proxy/demo", nested["http"])
	assert.Equal(t, "http://proxy", nested["https"])
}

func TestResolveConfig_StringList(t *testing.T) {
	in := map[string]interface{}{
		"a": []string{"{{ instance_name }}", "literal"},
		"b": []interface{}{"{{ instance_name }}-x"},
	}
	out := ResolveConfig(in, "demo")
	assert.Equal(t, []string{"demo", "literal"}, out["a"])
	assert.Equal(t, []interface{}{"demo-x"}, out["b"])
}

func TestResolveConfig_DoesNotMutateInput(t *testing.T) {
	in := map[string]interface{}{"name": "{{ instance_name }}"}
	_ = ResolveConfig(in, "demo")
	assert.Equal(t, "{{ instance_name }}", in["name"], "input must not be mutated")
}
