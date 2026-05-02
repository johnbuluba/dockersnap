package pluginsdk

import "time"

// PluginInput is the JSON object written to stdin for every command except
// init and schema.
type PluginInput struct {
	ContractVersion string                 `json:"contract_version"`
	Command         string                 `json:"command"`
	InstanceName    string                 `json:"instance_name"`
	Instance        Instance               `json:"instance"`
	ForwardedPorts  []ForwardedPort        `json:"forwarded_ports,omitempty"`
	Env             map[string]string      `json:"env,omitempty"`
	PluginConfig    map[string]interface{} `json:"plugin_config,omitempty"`
}

// Instance is the per-instance context surfaced to plugins.
type Instance struct {
	Name       string    `json:"name"`
	Socket     string    `json:"socket"`
	Subnet     string    `json:"subnet"`
	DataRoot   string    `json:"data_root"`
	HostVethIP string    `json:"host_veth_ip"`
	NsVethIP   string    `json:"ns_veth_ip"`
	NetnsName  string    `json:"netns_name"`
	MetalLBIP  string    `json:"metallb_ip"`
	CreatedAt  time.Time `json:"created_at"`
	CloneOf    string    `json:"clone_of,omitempty"`
}

// ForwardedPort is one entry in the host-side TCP proxy's mapping table.
//
// ContainerPort is the actual port the container exposes (e.g. 6443 for
// kind's API server, 5678 for hashicorp/http-echo). HostPort is the
// public-facing port the daemon's proxy listens on. Plugins that need to
// identify which forward corresponds to which of their config values
// match on ContainerPort.
type ForwardedPort struct {
	Label         string `json:"label"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol"`
}

// ConfigType enumerates the value types a plugin can declare for its config options.
type ConfigType string

// Constant names match the type prefix (ConfigType) so tygo's
// enum_style: "union" mode picks them up and emits a TS string-literal
// union in dashboard/src/lib/types/pluginsdk.ts. Without the prefix
// match, the generated TS would fall back to `type ConfigType = string`
// and lose compile-time exhaustiveness.
const (
	ConfigTypeString     ConfigType = "string"
	ConfigTypeInt        ConfigType = "int"
	ConfigTypeBool       ConfigType = "bool"
	ConfigTypeStringList ConfigType = "string-list"
	ConfigTypePath       ConfigType = "path"
)

// ConfigOption is one entry in a plugin's declared config schema.
type ConfigOption struct {
	Name        string      `json:"name"`
	Type        ConfigType  `json:"type"`
	Default     interface{} `json:"default,omitempty"`
	Required    bool        `json:"required,omitempty"`
	Description string      `json:"description,omitempty"`
}

// SchemaResponse is the JSON returned by a plugin's `schema` command.
type SchemaResponse struct {
	ContractVersion           string         `json:"contract_version"`
	SupportedContractVersions []string       `json:"supported_contract_versions"`
	PluginName                string         `json:"plugin_name"`
	PluginVersion             string         `json:"plugin_version"`
	Description               string         `json:"description,omitempty"`
	ConfigOptions             []ConfigOption `json:"config_options,omitempty"`
}

// ValidateResponse is the JSON returned by `validate`. Errors abort the deploy;
// warnings are logged but don't block.
type ValidateResponse struct {
	ContractVersion string   `json:"contract_version"`
	Valid           bool     `json:"valid"`
	Errors          []string `json:"errors,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

// ProgressEvent is one event in an NDJSON progress stream. Matches
// dockersnap's instance.ProgressEvent so plugin output can be forwarded
// to API clients verbatim.
type ProgressEvent struct {
	Step    string `json:"step"`
	Status  string `json:"status"` // "running", "done", "error"
	Message string `json:"message,omitempty"`
}

// AccessResponse is the JSON returned by `access`. Plugins emit token
// placeholders (${HOST}, ${PORT:label}, ${ACCESS_DIR}) in `Files[].Content`,
// `Env` values, and (computed) endpoint URLs; core resolves them.
type AccessResponse struct {
	ContractVersion string            `json:"contract_version"`
	Env             map[string]string `json:"env,omitempty"`
	Files           []File            `json:"files,omitempty"`
	Endpoints       []Endpoint        `json:"endpoints,omitempty"`
}

// File is one file the CLI will materialize under the access dir for the user.
type File struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Mode    string `json:"mode"` // octal string like "0600"
}

// Endpoint is a structured connection descriptor. The plugin emits the
// host_port_label; core resolves it to a concrete URL via ForwardedPorts.
type Endpoint struct {
	Name          string `json:"name"`
	Scheme        string `json:"scheme"`
	HostPortLabel string `json:"host_port_label,omitempty"`
	Insecure      bool   `json:"insecure,omitempty"`
	Description   string `json:"description,omitempty"`
	URL           string `json:"url,omitempty"` // populated by core after substitution
}

// DescribeResponse is the JSON returned by `describe`.
type DescribeResponse struct {
	ContractVersion string                 `json:"contract_version"`
	WorkloadType    string                 `json:"workload_type"`
	Status          string                 `json:"status"`
	Ports           []PortInfo             `json:"ports,omitempty"`
	Config          map[string]interface{} `json:"config,omitempty"`
	Details         map[string]interface{} `json:"details,omitempty"`
}

// PortInfo is one entry in a workload's advertised ports list.
type PortInfo struct {
	Label         string `json:"label"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

// HealthResponse is the JSON returned by `health`. Exit code is the source
// of truth for healthy/unhealthy; the JSON body is for diagnostics.
type HealthResponse struct {
	ContractVersion string        `json:"contract_version"`
	Healthy         bool          `json:"healthy"`
	Checks          []HealthCheck `json:"checks,omitempty"`
}

// HealthCheck is one entry in a HealthResponse's check list.
type HealthCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// ErrorResponse is the JSON envelope a plugin may write to stdout when
// it exits non-zero. If stdout is empty on failure, core uses captured
// stderr as the error message instead.
type ErrorResponse struct {
	ContractVersion string `json:"contract_version"`
	Error           string `json:"error"`
	Details         string `json:"details,omitempty"`
}
