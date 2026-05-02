// Query hooks. One per endpoint. Refetch cadence calibrated for a
// dashboard left open in a tab — frequent enough that you trust it,
// not so frequent it hammers the daemon.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type {
  AccessResponse,
  DaemonHealth,
  Instance,
  PluginInfo,
  PortsResponse,
  WorkloadHealth,
  WorkloadResponse,
} from "./types";

const REFETCH_MS = 5_000;

export function useDaemonHealth() {
  return useQuery<DaemonHealth>({
    queryKey: ["health"],
    queryFn: ({ signal }) => api<DaemonHealth>("/api/v1/health", { signal }),
    refetchInterval: REFETCH_MS,
  });
}

export function useInstances() {
  return useQuery<Instance[]>({
    queryKey: ["instances"],
    queryFn: ({ signal }) => api<Instance[]>("/api/v1/instances", { signal }),
    refetchInterval: REFETCH_MS,
  });
}

export function useInstance(name: string | undefined) {
  return useQuery<Instance>({
    queryKey: ["instances", name],
    queryFn: ({ signal }) => api<Instance>(`/api/v1/instances/${name}`, { signal }),
    enabled: !!name,
    refetchInterval: REFETCH_MS,
  });
}

export function usePlugins() {
  return useQuery<PluginInfo[]>({
    queryKey: ["plugins"],
    queryFn: ({ signal }) => api<PluginInfo[]>("/api/v1/plugins", { signal }),
    // Plugins change far less than instances — every 30s is plenty.
    refetchInterval: 30_000,
  });
}

// ── Per-instance plugin endpoints ────────────────────────────────────────
// /access / /workload / /workload/health are plugin invocations on the
// daemon — they're more expensive than /instances/{name}, so we don't poll
// them as aggressively. Manually invalidatable when actions complete.

export function useInstanceAccess(name: string | undefined) {
  return useQuery<AccessResponse>({
    queryKey: ["instances", name, "access"],
    queryFn: ({ signal }) =>
      api<AccessResponse>(`/api/v1/instances/${name}/access`, { signal }),
    enabled: !!name,
    refetchInterval: 30_000,
  });
}

export function useInstanceWorkload(name: string | undefined) {
  return useQuery<WorkloadResponse>({
    queryKey: ["instances", name, "workload"],
    queryFn: ({ signal }) =>
      api<WorkloadResponse>(`/api/v1/instances/${name}/workload`, { signal }),
    enabled: !!name,
    refetchInterval: 30_000,
  });
}

export function useInstanceHealth(name: string | undefined) {
  return useQuery<WorkloadHealth>({
    queryKey: ["instances", name, "health"],
    queryFn: ({ signal }) =>
      api<WorkloadHealth>(`/api/v1/instances/${name}/workload/health`, { signal }),
    enabled: !!name,
    refetchInterval: 15_000,
  });
}

export function useInstancePorts(name: string | undefined) {
  return useQuery<PortsResponse>({
    queryKey: ["instances", name, "ports"],
    queryFn: ({ signal }) =>
      api<PortsResponse>(`/api/v1/instances/${name}/ports`, { signal }),
    enabled: !!name,
    refetchInterval: 15_000,
  });
}

// Manual triggers — wired to refresh buttons in the UI.

export function useFreshHealth(name: string | undefined) {
  const qc = useQueryClient();
  return useMutation<WorkloadHealth, Error, void>({
    mutationFn: () =>
      api<WorkloadHealth>(`/api/v1/instances/${name}/workload/health?fresh=true`),
    onSuccess: (data) => {
      qc.setQueryData(["instances", name, "health"], data);
    },
  });
}

export function useRefreshPorts(name: string | undefined) {
  const qc = useQueryClient();
  return useMutation<PortsResponse, Error, void>({
    mutationFn: () =>
      api<PortsResponse>(`/api/v1/instances/${name}/ports/refresh`, {
        method: "POST",
      }),
    onSuccess: (data) => {
      qc.setQueryData(["instances", name, "ports"], data);
    },
  });
}

// Reload the plugin registry on the daemon (re-scan plugins.dir, re-run
// schema + init for each entry). Returns the fresh plugin list.
export function useReloadPlugins() {
  const qc = useQueryClient();
  return useMutation<PluginInfo[], Error, void>({
    mutationFn: () =>
      api<PluginInfo[]>("/api/v1/plugins/reload", { method: "POST" }),
    onSuccess: (data) => {
      qc.setQueryData(["plugins"], data);
    },
  });
}
