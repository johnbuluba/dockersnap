import { useState } from "preact/hooks";
import { usePlugins, useReloadPlugins } from "../lib/queries";
import type { ConfigOption, PluginInfo } from "../lib/types";
import { Code, ErrorPanel, PageHeader, Panel, Skeleton } from "../components/ui";
import { useDocumentTitle } from "../lib/title";

// Plugins admin: list of discovered plugin binaries, click a row to
// expand its schema (config options, digests, status). Reload button
// re-scans the daemon's plugin dir.
export function Plugins() {
  const { data, isLoading, error } = usePlugins();
  const reload = useReloadPlugins();
  const [expanded, setExpanded] = useState<string | null>(null);
  useDocumentTitle(["plugins"]);

  return (
    <section class="space-y-4">
      <PageHeader
        title="Plugins"
        subtitle="Discovered workload plugins on the daemon."
        action={
          <button
            type="button"
            onClick={() => reload.mutate()}
            disabled={reload.isPending}
            class="text-xs font-mono px-3 py-1.5 rounded-sm border border-border hover:border-border-strong text-text-secondary hover:text-text disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {reload.isPending ? "reloading…" : "reload"}
          </button>
        }
      />

      {reload.error && <ErrorPanel error={reload.error} />}
      {error && <ErrorPanel error={error} />}

      {isLoading ? (
        <Panel>
          <div class="px-4 py-6"><Skeleton class="w-full h-24" /></div>
        </Panel>
      ) : !data || data.length === 0 ? (
        <Panel>
          <div class="px-4 py-10 text-center space-y-2">
            <p class="text-sm text-text">No plugins discovered.</p>
            <p class="text-xs text-text-secondary">
              Drop plugin binaries into{" "}
              <code class="font-mono">/usr/local/lib/dockersnap/plugins/</code>{" "}
              and click reload.
            </p>
          </div>
        </Panel>
      ) : (
        <Panel class="overflow-hidden">
          <table class="w-full text-sm">
            <thead>
              <tr class="text-left text-xs uppercase tracking-wide text-text-muted font-mono border-b border-border">
                <th class="px-3 py-2 font-normal">status</th>
                <th class="px-3 py-2 font-normal">name</th>
                <th class="px-3 py-2 font-normal">version</th>
                <th class="px-3 py-2 font-normal">contract</th>
                <th class="px-3 py-2 font-normal">description</th>
              </tr>
            </thead>
            <tbody class="[&_tr:nth-child(even)]:bg-surface-subtle">
              {data.map((p) => (
                <PluginRow
                  key={p.name}
                  plugin={p}
                  expanded={expanded === p.name}
                  onToggle={() =>
                    setExpanded((cur) => (cur === p.name ? null : p.name))
                  }
                />
              ))}
            </tbody>
          </table>
        </Panel>
      )}
    </section>
  );
}

function PluginRow({
  plugin,
  expanded,
  onToggle,
}: {
  plugin: PluginInfo;
  expanded: boolean;
  onToggle: () => void;
}) {
  return (
    <>
      <tr
        onClick={onToggle}
        class="hover:bg-surface-hover cursor-pointer"
      >
        <td class="px-3 py-1.5">
          <span class="inline-flex items-center gap-2">
            <span
              class={`size-1.5 rounded-full ${
                plugin.status === "ready"
                  ? "bg-status-running"
                  : "bg-status-error"
              }`}
            />
            <span class="text-xs uppercase tracking-wide text-text-secondary font-mono">
              {plugin.status}
            </span>
          </span>
        </td>
        <td class="px-3 py-1.5 font-mono text-text">{plugin.name}</td>
        <td class="px-3 py-1.5 font-mono text-text-secondary">
          {plugin.version ?? <span class="text-text-muted">—</span>}
        </td>
        <td class="px-3 py-1.5 font-mono text-text-muted">
          {plugin.supported_contract_versions?.join(", ") ?? "—"}
        </td>
        <td class="px-3 py-1.5 text-text-secondary">
          {plugin.error ? (
            <span class="text-status-error font-mono text-xs">
              {plugin.error}
            </span>
          ) : (
            plugin.description ?? <span class="text-text-muted">—</span>
          )}
        </td>
      </tr>
      {expanded && (
        <tr class="bg-surface-subtle">
          <td colSpan={5} class="px-4 py-3">
            <PluginSchema plugin={plugin} />
          </td>
        </tr>
      )}
    </>
  );
}

function PluginSchema({ plugin }: { plugin: PluginInfo }) {
  return (
    <div class="space-y-3">
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
        {plugin.schema_digest && (
          <KvRow label="schema_digest" value={plugin.schema_digest} mono />
        )}
        {plugin.binary_digest && (
          <KvRow label="binary_digest" value={plugin.binary_digest} mono />
        )}
      </div>

      {plugin.config_options && plugin.config_options.length > 0 ? (
        <div>
          <p class="text-xs uppercase tracking-wide text-text-muted font-mono mb-1">
            config options
          </p>
          <div class="rounded-sm border border-border overflow-hidden">
            <table class="w-full text-sm">
              <thead>
                <tr class="text-left text-xs uppercase tracking-wide text-text-muted font-mono border-b border-border">
                  <th class="px-3 py-1.5 font-normal">name</th>
                  <th class="px-3 py-1.5 font-normal">type</th>
                  <th class="px-3 py-1.5 font-normal">default</th>
                  <th class="px-3 py-1.5 font-normal">description</th>
                </tr>
              </thead>
              <tbody class="[&_tr:nth-child(even)]:bg-surface">
                {plugin.config_options.map((opt) => (
                  <ConfigOptionRow key={opt.name} opt={opt} />
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : (
        <p class="text-xs text-text-muted">No declared config options.</p>
      )}
    </div>
  );
}

function ConfigOptionRow({ opt }: { opt: ConfigOption }) {
  return (
    <tr>
      <td class="px-3 py-1.5 font-mono text-text">
        {opt.name}
        {opt.required && (
          <span class="text-status-error text-xs ml-1">*</span>
        )}
      </td>
      <td class="px-3 py-1.5 font-mono text-text-secondary">{opt.type}</td>
      <td class="px-3 py-1.5 font-mono text-text-muted">
        {opt.default !== undefined && opt.default !== null
          ? String(opt.default)
          : "—"}
      </td>
      <td class="px-3 py-1.5 text-text-secondary">
        {opt.description ?? <span class="text-text-muted">—</span>}
      </td>
    </tr>
  );
}

function KvRow({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div class="flex flex-col gap-0.5">
      <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
        {label}
      </span>
      {mono ? <Code block>{value}</Code> : <span class="text-sm">{value}</span>}
    </div>
  );
}
