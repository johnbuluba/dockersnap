package plugin

import (
	"fmt"
	"os"
	"strings"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// ValidateConfig type-checks user-supplied plugin_config against a plugin's
// declared ConfigOptions. Returns the list of issues; nil means valid.
//
// Substitution of {{ instance_name }} happens in ResolveConfig before this
// runs — see that function below.
func ValidateConfig(opts []pluginsdk.ConfigOption, raw map[string]interface{}) []string {
	var issues []string
	declared := make(map[string]pluginsdk.ConfigOption, len(opts))
	for _, o := range opts {
		declared[o.Name] = o
	}

	// Reject keys not in the schema (typo prevention).
	for k := range raw {
		if _, ok := declared[k]; !ok {
			issues = append(issues, fmt.Sprintf("unknown config key %q", k))
		}
	}

	// Type-check declared keys + enforce required.
	for _, opt := range opts {
		v, present := raw[opt.Name]
		if !present {
			if opt.Required {
				issues = append(issues, fmt.Sprintf("missing required config key %q", opt.Name))
			}
			continue
		}
		if err := checkType(opt, v); err != nil {
			issues = append(issues, err.Error())
		}
	}

	return issues
}

func checkType(opt pluginsdk.ConfigOption, v interface{}) error {
	switch opt.Type {
	case pluginsdk.ConfigTypeString:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("config key %q: expected string, got %T", opt.Name, v)
		}
	case pluginsdk.ConfigTypeBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("config key %q: expected bool, got %T", opt.Name, v)
		}
	case pluginsdk.ConfigTypeInt:
		switch v.(type) {
		case int, int64, float64:
			// ok — JSON numbers come back as float64.
		default:
			return fmt.Errorf("config key %q: expected int, got %T", opt.Name, v)
		}
	case pluginsdk.ConfigTypeStringList:
		switch s := v.(type) {
		case []string:
		case []interface{}:
			for i, x := range s {
				if _, ok := x.(string); !ok {
					return fmt.Errorf("config key %q: element %d is not a string (%T)", opt.Name, i, x)
				}
			}
		default:
			return fmt.Errorf("config key %q: expected list of strings, got %T", opt.Name, v)
		}
	case pluginsdk.ConfigTypePath:
		path, ok := v.(string)
		if !ok {
			return fmt.Errorf("config key %q: expected path string, got %T", opt.Name, v)
		}
		if path == "" {
			if opt.Required {
				return fmt.Errorf("config key %q: empty path", opt.Name)
			}
			return nil
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("config key %q: file %s does not exist", opt.Name, path)
		}
	default:
		return fmt.Errorf("config key %q: unknown declared type %q", opt.Name, opt.Type)
	}
	return nil
}

// ResolveConfig substitutes the supported template tokens in any string-typed
// config values and returns a fresh map. The original is not mutated.
//
// Today only {{ instance_name }} is supported (per design v3 §4.6).
func ResolveConfig(raw map[string]interface{}, instanceName string) map[string]interface{} {
	out := make(map[string]interface{}, len(raw))
	for k, v := range raw {
		out[k] = resolveValue(v, instanceName)
	}
	return out
}

func resolveValue(v interface{}, instanceName string) interface{} {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, "{{ instance_name }}", instanceName)
	case []string:
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = strings.ReplaceAll(s, "{{ instance_name }}", instanceName)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, s := range x {
			out[i] = resolveValue(s, instanceName)
		}
		return out
	case map[string]interface{}:
		// Nested maps (e.g. proxy: {http: ..., https: ...}) — recurse.
		out := make(map[string]interface{}, len(x))
		for k, vv := range x {
			out[k] = resolveValue(vv, instanceName)
		}
		return out
	}
	return v
}
