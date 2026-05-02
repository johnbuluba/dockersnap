import { useMemo, useRef, useState } from "preact/hooks";
import { Link, useSearchParams } from "wouter-preact";
import { useInstances } from "../lib/queries";
import type { Instance, InstanceStatus } from "../lib/types";
import { ErrorPanel, PageHeader, Panel, Skeleton } from "../components/ui";
import { StatusLabel } from "../components/Status";
import { formatTime } from "../lib/format";
import { CreateModal } from "../components/modals/CreateModal";
import { useDocumentTitle } from "../lib/title";
import { useShortcuts } from "../lib/shortcuts";

const STATUS_OPTIONS: ReadonlyArray<InstanceStatus | "all"> = [
  "all",
  "running",
  "stopped",
  "error",
  "unknown",
];

export function Instances() {
  const { data, isLoading, error } = useInstances();
  const [searchParams, setSearchParams] = useSearchParams();
  const [createOpen, setCreateOpen] = useState(false);
  const filterInputRef = useRef<HTMLInputElement>(null);
  useDocumentTitle(["instances"]);
  useShortcuts({
    c: () => setCreateOpen(true),
    "/": () => filterInputRef.current?.focus(),
  });

  const statusFilter = (searchParams.get("status") as InstanceStatus | "all" | null) ?? "all";
  const workloadFilter = searchParams.get("has_workload"); // "1" / "0" / null
  const nameFilter = (searchParams.get("q") ?? "").trim().toLowerCase();

  const filtered = useMemo(() => {
    if (!data) return [];
    return data
      .filter((i) => statusFilter === "all" || i.status === statusFilter)
      .filter((i) =>
        workloadFilter === "1" ? !!i.workload_plugin :
        workloadFilter === "0" ? !i.workload_plugin :
        true,
      )
      .filter((i) => !nameFilter || i.name.toLowerCase().includes(nameFilter))
      .sort((a, b) => a.created_at.localeCompare(b.created_at));
  }, [data, statusFilter, workloadFilter, nameFilter]);

  return (
    <section class="space-y-4">
      <PageHeader
        title="Instances"
        subtitle="All dockersnap instances with status, network, and snapshot count."
        action={
          <button
            type="button"
            onClick={() => setCreateOpen(true)}
            class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
          >
            New instance
          </button>
        }
      />
      <CreateModal open={createOpen} onClose={() => setCreateOpen(false)} />

      {/* Filter bar — sharable via URL query string. */}
      <div class="flex flex-wrap items-center gap-3 text-sm">
        <div class="inline-flex items-center rounded-sm border border-border overflow-hidden">
          {STATUS_OPTIONS.map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => {
                setSearchParams((p) => {
                  if (s === "all") p.delete("status"); else p.set("status", s);
                  return p;
                });
              }}
              class={`px-3 py-1 text-xs font-mono uppercase tracking-wide border-r border-border last:border-r-0 ${
                statusFilter === s
                  ? "bg-accent-muted text-accent"
                  : "text-text-secondary hover:bg-surface-hover"
              }`}
            >
              {s}
            </button>
          ))}
        </div>

        <select
          value={workloadFilter ?? ""}
          onChange={(e) => {
            const v = (e.target as HTMLSelectElement).value;
            setSearchParams((p) => {
              if (v === "") p.delete("has_workload"); else p.set("has_workload", v);
              return p;
            });
          }}
          class="bg-bg border border-border rounded-sm px-2 py-1 text-xs font-mono text-text"
        >
          <option value="">all workloads</option>
          <option value="1">with workload</option>
          <option value="0">plain docker</option>
        </select>

        <input
          ref={filterInputRef}
          type="text"
          placeholder="filter by name…  (press /)"
          value={nameFilter}
          onInput={(e) => {
            const v = (e.target as HTMLInputElement).value;
            setSearchParams((p) => {
              if (v) p.set("q", v); else p.delete("q");
              return p;
            });
          }}
          class="font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none"
        />

        <span class="ml-auto text-xs text-text-muted font-mono tabular-nums">
          {data && `${filtered.length} / ${data.length}`}
        </span>
      </div>

      {error && <ErrorPanel error={error} />}

      <Panel class="overflow-hidden">
        {isLoading ? (
          <div class="px-4 py-6"><Skeleton class="w-full h-32" /></div>
        ) : filtered.length === 0 && (data?.length ?? 0) === 0 ? (
          <div class="px-4 py-10 text-center space-y-2">
            <p class="text-sm text-text">No instances yet.</p>
            <p class="text-xs text-text-secondary">Create one from the CLI:</p>
            <pre class="inline-block text-left text-sm font-mono bg-code-bg border border-border rounded-sm px-3 py-2 mt-1 text-text">
              dockersnap create dev --plugin kind
            </pre>
          </div>
        ) : filtered.length === 0 ? (
          <div class="px-4 py-10 text-center text-sm text-text-secondary">
            No instances match the current filters.
          </div>
        ) : (
          <table class="w-full text-sm">
            <thead>
              <tr class="text-left text-xs uppercase tracking-wide text-text-muted font-mono border-b border-border">
                <th class="px-3 py-2 font-normal">status</th>
                <th class="px-3 py-2 font-normal">name</th>
                <th class="px-3 py-2 font-normal">workload</th>
                <th class="px-3 py-2 font-normal">subnet</th>
                <th class="px-3 py-2 font-normal text-right">snapshots</th>
                <th class="px-3 py-2 font-normal">clone of</th>
                <th class="px-3 py-2 font-normal">created</th>
              </tr>
            </thead>
            <tbody class="[&_tr:nth-child(even)]:bg-surface-subtle">
              {filtered.map((inst) => <InstanceRow key={inst.name} inst={inst} />)}
            </tbody>
          </table>
        )}
      </Panel>
    </section>
  );
}

function InstanceRow({ inst }: { inst: Instance }) {
  return (
    <tr class="hover:bg-surface-hover">
      <td class="px-3 py-1.5"><StatusLabel status={inst.status} /></td>
      <td class="px-3 py-1.5">
        <Link
          href={`/instances/${inst.name}`}
          class="font-mono text-text hover:text-accent"
        >
          {inst.name}
        </Link>
      </td>
      <td class="px-3 py-1.5 font-mono text-text-secondary">
        {inst.workload_plugin ?? <span class="text-text-muted">—</span>}
      </td>
      <td class="px-3 py-1.5 font-mono text-text-secondary tabular-nums">{inst.subnet}</td>
      <td class="px-3 py-1.5 font-mono tabular-nums text-text-secondary text-right">
        {inst.snapshots.length}
      </td>
      <td class="px-3 py-1.5 font-mono text-text-secondary">
        {inst.clone_of ?? <span class="text-text-muted">—</span>}
      </td>
      <td class="px-3 py-1.5 font-mono text-text-muted text-xs tabular-nums">
        {formatTime(inst.created_at)}
      </td>
    </tr>
  );
}
