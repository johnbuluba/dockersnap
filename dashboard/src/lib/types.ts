// API types — generated from Go via tygo (`task ui:gen-types`).
// This file is the public surface the rest of the dashboard imports
// from; everything in ./types/* is generated and shouldn't be edited
// directly.
//
// Re-exports the wire types under their canonical names plus a few
// UI-only aliases / unions where the Go side is wider than the
// dashboard needs (e.g. InstanceStatus narrows state.Status to the
// known values for switch exhaustiveness).

// ── Re-exports straight from the generated files ──────────────────────────

export type { Instance, SnapshotInfo as Snapshot } from "./types/state";
export type {
  AccessResponse,
  File as AccessFile,
  Endpoint as AccessEndpoint,
  HealthCheck,
  HealthResponse,
  PortInfo,
  ConfigOption,
  ConfigType,
  DescribeResponse,
} from "./types/pluginsdk";
export type { PortMapping as ForwardedPort, InstancePorts as PortsResponse } from "./types/proxy";
export type { ProgressEvent } from "./types/instance";
export type {
  DaemonHealth,
  PluginInfo,
  WorkloadDescribeResponse,
  WorkloadHealthResponse,
} from "./types/api";

// ── UI-only aliases ───────────────────────────────────────────────────────

// Go's state.Status is `type Status = string` with run-time constants.
// At the wire level we only see these four values; narrow for switch
// exhaustiveness in components like StatusDot.
export type InstanceStatus = "running" | "stopped" | "error" | "unknown";

// Backwards-compatible names for the renamed API types so existing
// page/component imports keep working without a churn pass.
export type { WorkloadDescribeResponse as WorkloadDescribe } from "./types/api";
export type { WorkloadHealthResponse as WorkloadHealth } from "./types/api";
import type { WorkloadDescribeResponse } from "./types/api";
export type WorkloadResponse = WorkloadDescribeResponse;
