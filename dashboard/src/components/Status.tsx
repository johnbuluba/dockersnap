import type { InstanceStatus } from "../lib/types";

// Visual primitives for instance status. Per design language §3b:
// dots, not pills; status colors live on their own conceptual hemispheres.

const dotClass: Record<InstanceStatus, string> = {
  running: "bg-status-running",
  stopped: "bg-status-stopped",
  error:   "bg-status-error",
  unknown: "bg-status-unknown",
};

// Accept any string at the prop boundary because the wire type
// (state.Status from Go via tygo) is just `string`. Anything we don't
// recognize falls back to "unknown" — better than crashing if the
// daemon ever adds a new status value.
function narrow(s: string): InstanceStatus {
  if (s === "running" || s === "stopped" || s === "error") return s;
  return "unknown";
}

/** A 6-px circle in the named status color. */
export function StatusDot({ status }: { status: string }) {
  const s = narrow(status);
  return (
    <span
      class={`inline-block size-1.5 rounded-full align-middle ${dotClass[s]}`}
      aria-label={s}
    />
  );
}

/** Dot + uppercase label, the standard status cell content. */
export function StatusLabel({ status }: { status: string }) {
  const s = narrow(status);
  return (
    <span class="inline-flex items-center gap-2">
      <StatusDot status={s} />
      <span class="text-text-secondary text-xs uppercase tracking-wide font-mono">
        {s}
      </span>
    </span>
  );
}

/** Workload-health pill. Used in the Overview's daemon card. */
export function HealthBadge({
  healthy,
  unhealthy,
}: {
  healthy: number;
  unhealthy: number;
}) {
  const total = healthy + unhealthy;
  if (total === 0) {
    return (
      <span class="text-xs text-text-muted font-mono">no workloads tracked</span>
    );
  }
  const allHealthy = unhealthy === 0;
  return (
    <span class="inline-flex items-center gap-2 text-xs font-mono">
      <span class={`size-1.5 rounded-full ${allHealthy ? "bg-status-running" : "bg-status-error"}`} />
      <span class="text-text-secondary">
        {healthy} healthy
        {unhealthy > 0 && (
          <span class="text-status-error"> · {unhealthy} unhealthy</span>
        )}
      </span>
    </span>
  );
}
