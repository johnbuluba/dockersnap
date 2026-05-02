import { Link } from "wouter-preact";
import { useDaemonHealth, useInstances } from "../lib/queries";
import { Code, ErrorPanel, Field, PageHeader, Panel, Skeleton } from "../components/ui";
import { HealthBadge, StatusDot } from "../components/Status";
import { relativeTime } from "../lib/format";
import { useDocumentTitle } from "../lib/title";

export function Overview() {
  const health = useDaemonHealth();
  const instances = useInstances();
  useDocumentTitle([]);

  return (
    <section class="space-y-4">
      <PageHeader
        title="Overview"
        subtitle="Daemon health, instance summary, plugin status."
      />

      {/* Daemon card — version, uptime, totals. */}
      <Panel>
        <div class="px-4 py-3 grid grid-cols-2 sm:grid-cols-4 gap-4">
          <Field label="version">
            {health.isLoading ? <Skeleton class="w-24 h-4" /> :
             health.error ? <span class="text-status-error font-mono">unreachable</span> :
             <Code>{health.data?.version}</Code>}
          </Field>
          <Field label="uptime">
            {health.isLoading ? <Skeleton class="w-20 h-4" /> :
             health.error ? <Code>—</Code> :
             <Code>{health.data?.uptime}</Code>}
          </Field>
          <Field label="instances">
            {health.isLoading ? <Skeleton class="w-12 h-4" /> :
             health.error ? <Code>—</Code> :
             <span class="font-mono tabular-nums text-text">
               {health.data?.running}<span class="text-text-muted"> / {health.data?.instances}</span>
             </span>}
          </Field>
          <Field label="workloads">
            {health.isLoading ? <Skeleton class="w-32 h-4" /> :
             health.error ? <Code>—</Code> :
             <HealthBadge
               healthy={health.data?.running_healthy ?? 0}
               unhealthy={health.data?.running_unhealthy ?? 0}
             />}
          </Field>
        </div>
      </Panel>

      {health.error && <ErrorPanel error={health.error} />}

      {/* Recent instances — top 5 by created_at descending. */}
      <Panel>
        <div class="px-4 py-2 border-b border-border flex items-center justify-between">
          <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
            Recent instances
          </span>
          <Link href="/instances" class="text-xs text-accent hover:text-accent-hover">
            view all →
          </Link>
        </div>
        {instances.isLoading ? (
          <div class="px-4 py-6"><Skeleton class="w-full h-20" /></div>
        ) : instances.error ? (
          <div class="px-4 py-3"><ErrorPanel error={instances.error} /></div>
        ) : (instances.data?.length ?? 0) === 0 ? (
          <div class="px-4 py-8 text-center space-y-2">
            <p class="text-sm text-text-secondary">No instances yet.</p>
            <pre class="inline-block text-left text-sm font-mono bg-code-bg border border-border rounded-sm px-3 py-2 text-text">
              dockersnap create dev --plugin kind
            </pre>
          </div>
        ) : (
          <ul class="divide-y divide-border">
            {[...(instances.data ?? [])]
              .sort((a, b) => b.created_at.localeCompare(a.created_at))
              .slice(0, 5)
              .map((inst) => (
                <li key={inst.name} class="px-4 py-2 flex items-center gap-3 hover:bg-surface-hover">
                  <StatusDot status={inst.status} />
                  <Link
                    href={`/instances/${inst.name}`}
                    class="font-mono text-sm text-text hover:text-accent"
                  >
                    {inst.name}
                  </Link>
                  {inst.workload_plugin && (
                    <span class="text-xs text-text-muted font-mono">
                      · {inst.workload_plugin}
                    </span>
                  )}
                  <span class="ml-auto text-xs text-text-muted tabular-nums">
                    {relativeTime(inst.created_at)}
                  </span>
                </li>
              ))}
          </ul>
        )}
      </Panel>
    </section>
  );
}
