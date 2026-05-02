package pluginsdk

import (
	"fmt"
	"log/slog"
	"os"
)

// Context is the parsed PluginInput passed to lifecycle handlers.
type Context struct {
	InstanceName   string
	Instance       Instance
	ForwardedPorts []ForwardedPort
	Env            map[string]string
	Config         *Config

	// Logger is a *slog.Logger that emits NDJSON LogEntry lines to stderr;
	// the daemon's plugin runner re-emits them into its own slog. The plugin
	// name and instance name are auto-attached, so handler code can just
	// call ctx.Logger.Info("…", "k", v).
	Logger *slog.Logger
}

// ForwardedPort returns the entry whose label matches, or false if none.
func (c *Context) ForwardedPort(label string) (ForwardedPort, bool) {
	for _, p := range c.ForwardedPorts {
		if p.Label == label {
			return p, true
		}
	}
	return ForwardedPort{}, false
}

// Config is a typed accessor over plugin_config, validated against the
// plugin's declared ConfigOptions.
//
// Methods panic if the requested key isn't declared in ConfigOptions or if
// the declared type doesn't match the called accessor — that's a plugin
// authoring bug, caught in tests. Use OptString/OptInt/etc. for keys that
// genuinely may be unset at runtime.
type Config struct {
	raw     map[string]interface{}
	options map[string]ConfigOption
}

func newConfig(raw map[string]interface{}, opts []ConfigOption) *Config {
	m := make(map[string]ConfigOption, len(opts))
	for _, o := range opts {
		m[o.Name] = o
	}
	return &Config{raw: raw, options: m}
}

// NewConfigForTest is the test-only constructor for *Config. Production code
// gets a *Config built from PluginInput by the Runner; tests use this helper
// (typically via sdktest.NewContext) to drive handlers in-process.
func NewConfigForTest(raw map[string]interface{}, opts []ConfigOption) *Config {
	return newConfig(raw, opts)
}

func (c *Config) optionOrPanic(key string, want ConfigType) ConfigOption {
	opt, ok := c.options[key]
	if !ok {
		panic(fmt.Sprintf("pluginsdk: config key %q not declared in ConfigOptions", key))
	}
	if opt.Type != want {
		panic(fmt.Sprintf("pluginsdk: config key %q declared as %s, accessed as %s",
			key, opt.Type, want))
	}
	return opt
}

// String returns a required string config value, falling back to the declared
// default when the user hasn't set it.
func (c *Config) String(key string) string {
	opt := c.optionOrPanic(key, ConfigTypeString)
	if v, ok := c.raw[key].(string); ok && v != "" {
		return v
	}
	if d, ok := opt.Default.(string); ok {
		return d
	}
	return ""
}

// OptString returns a string value plus whether it was explicitly set
// (true even if the user typed an empty string; false only if absent and
// no default applies).
func (c *Config) OptString(key string) (string, bool) {
	c.optionOrPanic(key, ConfigTypeString)
	if v, ok := c.raw[key]; ok {
		s, _ := v.(string)
		return s, true
	}
	if opt := c.options[key]; opt.Default != nil {
		if d, ok := opt.Default.(string); ok {
			return d, true
		}
	}
	return "", false
}

// Int returns an int config value, falling back to the declared default.
// Accepts both float64 (JSON numbers) and int.
func (c *Config) Int(key string) int {
	opt := c.optionOrPanic(key, ConfigTypeInt)
	switch v := c.raw[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	switch d := opt.Default.(type) {
	case int:
		return d
	case int64:
		return int(d)
	case float64:
		return int(d)
	}
	return 0
}

// Bool returns a bool config value, falling back to the declared default.
func (c *Config) Bool(key string) bool {
	opt := c.optionOrPanic(key, ConfigTypeBool)
	if v, ok := c.raw[key].(bool); ok {
		return v
	}
	if d, ok := opt.Default.(bool); ok {
		return d
	}
	return false
}

// Path returns a string config value typed as a filesystem path.
// Existence is verified by core during config validation, so handlers
// can rely on the file being present at handler-invocation time.
func (c *Config) Path(key string) string {
	opt := c.optionOrPanic(key, ConfigTypePath)
	if v, ok := c.raw[key].(string); ok && v != "" {
		return v
	}
	if d, ok := opt.Default.(string); ok {
		return d
	}
	return ""
}

// OptPath is the present/absent variant of Path. Returns ("", false) when
// the user didn't set it and there's no default.
func (c *Config) OptPath(key string) (string, bool) {
	c.optionOrPanic(key, ConfigTypePath)
	if v, ok := c.raw[key]; ok {
		s, _ := v.(string)
		return s, true
	}
	if opt := c.options[key]; opt.Default != nil {
		if d, ok := opt.Default.(string); ok {
			return d, true
		}
	}
	return "", false
}

// StringList returns a []string config value, falling back to the declared
// default. Accepts both []string and []interface{} of strings (JSON unmarshal).
func (c *Config) StringList(key string) []string {
	c.optionOrPanic(key, ConfigTypeStringList)
	if v, ok := c.raw[key]; ok {
		return toStringSlice(v)
	}
	if opt := c.options[key]; opt.Default != nil {
		return toStringSlice(opt.Default)
	}
	return nil
}

// Raw returns the raw config map. Use only when the typed accessors don't
// fit (e.g. nested maps the plugin schema doesn't model).
func (c *Config) Raw() map[string]interface{} {
	return c.raw
}

func toStringSlice(v interface{}) []string {
	switch s := v.(type) {
	case []string:
		out := make([]string, len(s))
		copy(out, s)
		return out
	case []interface{}:
		out := make([]string, 0, len(s))
		for _, x := range s {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

// MustGetenv returns os.Getenv(name) or aborts the plugin with a clear error.
// Useful for exec-passthrough commands that read DOCKERSNAP_* env vars.
//
// Reserved for future use once exec passthrough lands; currently
// lifecycle commands use Context, not env vars.
func MustGetenv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fatalf("required environment variable %q is not set", name)
	}
	return v
}
