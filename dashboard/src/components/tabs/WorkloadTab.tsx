import { useFreshHealth, useInstanceHealth, useInstanceWorkload } from "../../lib/queries";
import type { WorkloadDescribe, WorkloadHealth } from "../../lib/types";
import { Code, ErrorPanel, Field, Panel, Skeleton } from "../ui";

// Plugin describe + health for the bound workload. No-workload instances
// get a friendly empty state explaining that.
export function WorkloadTab({ instanceName }: { instanceName: string }) {
  const wl = useInstanceWorkload(instanceName);
  const health = useInstanceHealth(instanceName);
  const freshen = useFreshHealth(instanceName);

  if (wl.isLoading) {
    return (
      <Panel>
        <div class="px-4 py-6"><Skeleton class="w-full h-32" /></div>
      </Panel>
    );
  }
  if (wl.error) return <ErrorPanel error={wl.error} />;
  if (!wl.data) return null;

  // No-workload synthetic body has workload_type="none".
  if ("workload_plugin" in wl.data && wl.data.workload_plugin === "") {
    return (
      <Panel>
        <div class="px-4 py-10 text-center text-sm text-text-secondary">
          No workload bound to this instance.
        </div>
      </Panel>
    );
  }

  const desc = wl.data as WorkloadDescribe;

  return (
    <div class="space-y-3">
      <Panel>
        <div class="px-3 py-2 border-b border-border">
          <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
            describe
          </span>
        </div>
        <div class="px-3 py-3 grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3">
          <Field label="type"><Code>{desc.workload_type}</Code></Field>
          <Field label="status"><Code>{desc.status}</Code></Field>
          {desc.config && Object.keys(desc.config).length > 0 && (
            <Field label="config">
              <KvList map={desc.config} />
            </Field>
          )}
          {desc.details && Object.keys(desc.details).length > 0 && (
            <Field label="details">
              <KvList map={desc.details} />
            </Field>
          )}
          {desc.ports && desc.ports.length > 0 && (
            <Field label="advertised ports">
              <ul class="space-y-0.5">
                {desc.ports.map((p) => (
                  <li key={p.label} class="font-mono text-sm">
                    {p.label}{" "}
                    <span class="text-text-muted">{p.container_port}/{p.protocol}</span>
                  </li>
                ))}
              </ul>
            </Field>
          )}
        </div>
      </Panel>

      <Panel>
        <div class="px-3 py-2 border-b border-border flex items-center justify-between">
          <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
            health
          </span>
          <button
            type="button"
            onClick={() => freshen.mutate()}
            disabled={freshen.isPending}
            class="text-xs font-mono px-2 py-0.5 rounded-sm border border-border hover:border-border-strong text-text-secondary hover:text-text disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {freshen.isPending ? "refreshing…" : "refresh"}
          </button>
        </div>
        <HealthBody
          data={health.data}
          isLoading={health.isLoading}
          error={health.error}
        />
      </Panel>
    </div>
  );
}

function HealthBody({
  data,
  isLoading,
  error,
}: {
  data: WorkloadHealth | undefined;
  isLoading: boolean;
  error: Error | null;
}) {
  if (isLoading) {
    return <div class="px-4 py-6"><Skeleton class="w-full h-16" /></div>;
  }
  if (error) {
    return <div class="px-3 py-3"><ErrorPanel error={error} /></div>;
  }
  if (!data) return null;
  if (data.workload_plugin === "") {
    return (
      <div class="px-4 py-6 text-center text-sm text-text-secondary">
        No workload bound — nothing to check.
      </div>
    );
  }
  if (!data.checks || data.checks.length === 0) {
    return (
      <div class="px-4 py-6 text-center text-sm text-text-secondary">
        Plugin reported {data.healthy ? "healthy" : "unhealthy"} with no diagnostic checks.
      </div>
    );
  }
  return (
    <ul class="divide-y divide-border">
      {data.checks.map((c) => (
        <li
          key={c.name}
          class="px-3 py-2 flex items-center gap-3 text-sm"
        >
          <span
            class={`shrink-0 size-1.5 rounded-full ${
              c.ok ? "bg-status-running" : "bg-status-error"
            }`}
          />
          <span class="font-mono text-text">{c.name}</span>
          <span class="text-text-secondary ml-auto break-words text-right">
            {c.message ?? (c.ok ? "ok" : "fail")}
          </span>
        </li>
      ))}
    </ul>
  );
}

function KvList({ map }: { map: Record<string, unknown> }) {
  return (
    <ul class="space-y-0.5">
      {Object.entries(map).map(([k, v]) => (
        <li key={k} class="text-sm font-mono">
          <span class="text-text-secondary">{k}</span>
          <span class="text-text-muted"> = </span>
          <span class="text-text">{formatValue(v)}</span>
        </li>
      ))}
    </ul>
  );
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return "—";
  if (typeof v === "string") return v;
  return JSON.stringify(v);
}
