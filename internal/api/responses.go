package api

import "github.com/johnbuluba/dockersnap/pkg/pluginsdk"

// JSON response types for endpoints that historically returned ad-hoc
// `map[string]interface{}` shapes. Promoting them to named structs gives
// `tygo` something to walk for TS-type generation, and makes the API
// surface explicit (the structs are the contract).
//
// Field tags use snake_case to match the existing wire format. Don't
// rename a field without a corresponding dashboard adjustment — the
// generated TS will move with it, but third-party callers won't.

// DaemonHealth is the body of GET /api/v1/health.
type DaemonHealth struct {
	Status           string `json:"status"`
	Version          string `json:"version"`
	Uptime           string `json:"uptime"`
	StartedAt        string `json:"started_at"`
	Instances        int    `json:"instances"`
	Running          int    `json:"running"`
	RunningHealthy   int    `json:"running_healthy"`
	RunningUnhealthy int    `json:"running_unhealthy"`
}

// WorkloadDescribeResponse is the body of GET /api/v1/instances/{name}/workload.
//
// Fields are explicit (rather than embedding pluginsdk.DescribeResponse)
// because tygo doesn't see through anonymous-field embeds — it would
// emit the embed as `any` in TS. The wire shape is identical to the
// embed version since Go's JSON marshaler flattens anonymous fields too.
type WorkloadDescribeResponse struct {
	WorkloadPlugin  string                 `json:"workload_plugin,omitempty"`
	WorkloadType    string                 `json:"workload_type,omitempty"`
	ContractVersion string                 `json:"contract_version,omitempty"`
	Status          string                 `json:"status,omitempty"`
	Ports           []pluginsdk.PortInfo   `json:"ports,omitempty"`
	Config          map[string]interface{} `json:"config,omitempty"`
	Details         map[string]interface{} `json:"details,omitempty"`
}

// WorkloadHealthResponse is the body of GET /api/v1/instances/{name}/workload/health.
//
// Three shapes are unified here:
//
//   - No-workload instance: WorkloadPlugin="", Healthy=true, Checks=[{
//     Name:"workload", OK:true, Message:"no workload bound"}].
//   - Cached entry: WorkloadPlugin set, CheckedAt + ConsecutiveFails
//     populated, Checks lifted from the cached HealthResponse.
//   - Fresh poll (?fresh=true): no cache fields, Healthy + Checks
//     come straight from the plugin.
//
// Callers (CLI, dashboard) read the same top-level fields regardless.
type WorkloadHealthResponse struct {
	WorkloadPlugin   string                 `json:"workload_plugin"`
	Healthy          bool                   `json:"healthy"`
	Checks           []pluginsdk.HealthCheck `json:"checks,omitempty"`
	ContractVersion  string                 `json:"contract_version,omitempty"`
	CheckedAt        string                 `json:"checked_at,omitempty"`
	ConsecutiveFails int                    `json:"consecutive_fails,omitempty"`
}
