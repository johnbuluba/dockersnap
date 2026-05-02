package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the dockersnap configuration loaded from /etc/dockersnap/config.yaml.
type Config struct {
	ZFS     ZFSConfig     `yaml:"zfs"`
	API     APIConfig     `yaml:"api"`
	Network NetworkConfig `yaml:"network"`
	Docker  DockerConfig  `yaml:"docker"`
	Plugins PluginsConfig `yaml:"plugins"`

	StateFile string `yaml:"state_file"`
	RunDir    string `yaml:"run_dir"`
	LogLevel  string `yaml:"log_level"` // debug, info, warn, error (default: info)
}

type ZFSConfig struct {
	Pool          string `yaml:"pool"`
	DatasetPrefix string `yaml:"dataset_prefix"`
	ArcMaxBytes   int64  `yaml:"arc_max_bytes"`
}

type APIConfig struct {
	Listen    string `yaml:"listen"`
	Token     string `yaml:"token"`
	ProxyBind string `yaml:"proxy_bind"` // Address for TCP port forwarding (default: "0.0.0.0")
}

type NetworkConfig struct {
	Range             string `yaml:"range"`
	SubnetSize        int    `yaml:"subnet_size"`
	BaseOffset        string `yaml:"base_offset"`
	MetalLBHostOffset string `yaml:"metallb_host_offset"`
	HostInterface     string `yaml:"host_interface,omitempty"` // Physical NIC for NAT; auto-detected if empty
}

type DockerConfig struct {
	DNS        []string    `yaml:"dns"`
	LogMaxSize string      `yaml:"log_max_size"`
	LogMaxFile int         `yaml:"log_max_file"`
	Proxy      ProxyConfig `yaml:"proxy"`
}

type ProxyConfig struct {
	HTTP    string `yaml:"http"`
	HTTPS   string `yaml:"https"`
	NoProxy string `yaml:"no_proxy"`
}

// PluginsConfig configures the workload-plugin subsystem.
// See docs/PLUGIN-DESIGN.md.
type PluginsConfig struct {
	// Dir is the directory where plugin binaries live.
	// Default: /usr/local/lib/dockersnap/plugins.
	Dir string `yaml:"dir"`

	// HealthPollInterval is how often core polls each running instance's
	// plugin `health` command. Default: 30s. Set to "0s" to disable polling
	// (health becomes pull-only via API).
	HealthPollInterval string `yaml:"health_poll_interval"`

	// HealthFailureThreshold is the number of consecutive failed polls before
	// an instance's cached health flips to unhealthy. Default: 3.
	HealthFailureThreshold int `yaml:"health_failure_threshold"`

	// Timeouts maps lifecycle command name → timeout. Empty entries fall
	// back to internal defaults.
	Timeouts PluginTimeouts `yaml:"timeouts"`
}

// PluginTimeouts mirrors plugin.Timeouts as YAML strings (parsed via time.ParseDuration).
type PluginTimeouts struct {
	Init     string `yaml:"init"`
	Schema   string `yaml:"schema"`
	Validate string `yaml:"validate"`
	Deploy   string `yaml:"deploy"`
	Teardown string `yaml:"teardown"`
	Access   string `yaml:"access"`
	Describe string `yaml:"describe"`
	Health   string `yaml:"health"`
}

// Defaults returns a Config with sensible defaults.
func Defaults() *Config {
	return &Config{
		ZFS: ZFSConfig{
			Pool:          "dockersnap",
			DatasetPrefix: "instances",
			ArcMaxBytes:   5368709120, // 5GB
		},
		API: APIConfig{
			Listen:    "127.0.0.1:9847",
			ProxyBind: "127.0.0.1",
		},
		Network: NetworkConfig{
			Range:             "10.0.0.0/8",
			SubnetSize:        16,
			BaseOffset:        "10.10.0.0",
			MetalLBHostOffset: "10.10",
		},
		Docker: DockerConfig{
			// DNS empty by default — the netns dnsmasq + host upstream resolvers
			// (resolved via /run/systemd/resolve/resolv.conf or /etc/resolv.conf)
			// give containers working DNS without operator config. Operators
			// can add additional resolvers via docker.dns in config.yaml.
			DNS:        nil,
			LogMaxSize: "50m",
			LogMaxFile: 3,
		},
		Plugins: PluginsConfig{
			Dir:                    "/usr/local/lib/dockersnap/plugins",
			HealthPollInterval:     "30s",
			HealthFailureThreshold: 3,
		},
		StateFile: "/var/lib/dockersnap/state.json",
		RunDir:    "/run/dockersnap",
	}
}

// Load reads and parses the configuration file. A missing file returns
// defaults — not an error.
func Load(path string) (*Config, error) {
	cfg := Defaults()

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading config file %s: %w", path, err)
		}
	} else if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	return cfg, nil
}

// DatasetPath returns the full ZFS dataset path for an instance.
func (c *Config) DatasetPath(instanceName string) string {
	return fmt.Sprintf("%s/%s/%s", c.ZFS.Pool, c.ZFS.DatasetPrefix, instanceName)
}

// MountPoint returns the filesystem mount point for an instance dataset.
func (c *Config) MountPoint(instanceName string) string {
	return fmt.Sprintf("/%s/%s/%s", c.ZFS.Pool, c.ZFS.DatasetPrefix, instanceName)
}

// SocketPath returns the Docker socket path for an instance.
func (c *Config) SocketPath(instanceName string) string {
	return fmt.Sprintf("%s/%s.sock", c.RunDir, instanceName)
}

// PidFilePath returns the Docker PID file path for an instance.
func (c *Config) PidFilePath(instanceName string) string {
	return fmt.Sprintf("%s/%s.pid", c.RunDir, instanceName)
}

// SlogLevel returns the slog.Level corresponding to the configured log_level.
// Environment variable DOCKERSNAP_LOG_LEVEL overrides the config file value.
func (c *Config) SlogLevel() slog.Level {
	level := c.LogLevel
	if env := os.Getenv("DOCKERSNAP_LOG_LEVEL"); env != "" {
		level = env
	}
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
