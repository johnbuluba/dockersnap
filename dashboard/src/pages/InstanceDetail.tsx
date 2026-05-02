import { useState } from "preact/hooks";
import { Link, useLocation, useParams, useSearchParams } from "wouter-preact";
import { useInstance } from "../lib/queries";
import {
  Code,
  ErrorPanel,
  Field,
  PageHeader,
  Panel,
  Skeleton,
} from "../components/ui";
import { StatusLabel } from "../components/Status";
import { formatTime, relativeTime } from "../lib/format";
import { SnapshotModal } from "../components/modals/SnapshotModal";
import { RevertModal } from "../components/modals/RevertModal";
import { CloneModal } from "../components/modals/CloneModal";
import { DeleteModal } from "../components/modals/DeleteModal";
import { LifecycleModal, type LifecycleAction } from "../components/modals/LifecycleModal";
import { AccessTab } from "../components/tabs/AccessTab";
import { WorkloadTab } from "../components/tabs/WorkloadTab";
import { PortsTab } from "../components/tabs/PortsTab";
import { useDocumentTitle } from "../lib/title";
import type { Instance } from "../lib/types";

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "snapshots", label: "Snapshots" },
  { id: "access", label: "Access" },
  { id: "workload", label: "Workload" },
  { id: "ports", label: "Ports" },
] as const;

type TabId = (typeof TABS)[number]["id"];

export function InstanceDetail() {
  const { name } = useParams<{ name: string }>();
  const { data: inst, isLoading, error } = useInstance(name);
  const [searchParams, setSearchParams] = useSearchParams();
  const [, setLocation] = useLocation();
  const tab = (searchParams.get("tab") as TabId | null) ?? "overview";
  const [snapshotOpen, setSnapshotOpen] = useState(false);
  const [revertLabel, setRevertLabel] = useState<string | undefined>();
  const [cloneLabel, setCloneLabel] = useState<string | undefined>();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [lifecycle, setLifecycle] = useState<LifecycleAction | null>(null);
  useDocumentTitle(["instances", name]);

  return (
    <section class="space-y-4">
      <div class="space-y-1">
        <Link
          href="/instances"
          class="text-xs text-text-muted hover:text-text font-mono"
        >
          ← instances
        </Link>
        <PageHeader
          title={name ?? ""}
          subtitle={
            inst
              ? `Created ${relativeTime(inst.created_at)} · ${inst.dataset}`
              : undefined
          }
          action={
            inst && (
              <div class="flex items-center gap-2">
                <StatusLabel status={inst.status} />
                {inst.status === "running" ? (
                  <>
                    <SecondaryButton onClick={() => setLifecycle("restart")}>
                      Restart
                    </SecondaryButton>
                    <SecondaryButton onClick={() => setLifecycle("stop")}>
                      Stop
                    </SecondaryButton>
                  </>
                ) : (
                  <SecondaryButton onClick={() => setLifecycle("start")}>
                    Start
                  </SecondaryButton>
                )}
                <button
                  type="button"
                  onClick={() => setSnapshotOpen(true)}
                  class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
                >
                  Snapshot
                </button>
                <button
                  type="button"
                  onClick={() => setDeleteOpen(true)}
                  class="border border-status-error text-status-error hover:bg-status-error hover:text-white px-3 py-1.5 rounded-sm text-sm font-medium"
                >
                  Delete
                </button>
              </div>
            )
          }
        />
      </div>

      {inst && (
        <>
          <SnapshotModal
            open={snapshotOpen}
            instanceName={inst.name}
            onClose={() => setSnapshotOpen(false)}
          />
          <RevertModal
            open={revertLabel !== undefined}
            instanceName={inst.name}
            snapshots={inst.snapshots}
            preselect={revertLabel}
            onClose={() => setRevertLabel(undefined)}
          />
          <CloneModal
            open={cloneLabel !== undefined}
            instanceName={inst.name}
            snapshots={inst.snapshots}
            preselect={cloneLabel}
            onClose={() => setCloneLabel(undefined)}
          />
          <DeleteModal
            open={deleteOpen}
            instanceName={inst.name}
            onClose={() => setDeleteOpen(false)}
            onSuccess={() => setLocation("/instances")}
          />
          <LifecycleModal
            open={lifecycle !== null}
            action={lifecycle ?? "start"}
            instanceName={inst.name}
            onClose={() => setLifecycle(null)}
          />
        </>
      )}

      {/* Tab strip — wouter's useSearchParams keeps state shareable. */}
      <nav class="flex gap-1 border-b border-border text-sm">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => {
              setSearchParams((p) => {
                if (t.id === "overview") p.delete("tab"); else p.set("tab", t.id);
                return p;
              });
            }}
            class={`px-3 py-1.5 text-sm font-mono border-b -mb-px ${
              tab === t.id
                ? "border-accent text-accent"
                : "border-transparent text-text-secondary hover:text-text"
            }`}
          >
            {t.label}
          </button>
        ))}
      </nav>

      {isLoading && <Skeleton class="w-full h-40" />}
      {error && <ErrorPanel error={error} />}

      {inst && (
        <>
          {tab === "overview" && <OverviewTab inst={inst} />}
          {tab === "snapshots" && (
            <SnapshotsTab
              inst={inst}
              onRevert={(label) => setRevertLabel(label)}
              onClone={(label) => setCloneLabel(label)}
            />
          )}
          {tab === "access" && <AccessTab instanceName={inst.name} />}
          {tab === "workload" && <WorkloadTab instanceName={inst.name} />}
          {tab === "ports" && <PortsTab instanceName={inst.name} />}
        </>
      )}
    </section>
  );
}

function OverviewTab({ inst }: { inst: Instance }) {
  return (
    <Panel>
      <div class="px-4 py-3 grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3">
        <Field label="status"><StatusLabel status={inst.status} /></Field>
        <Field label="created"><Code>{formatTime(inst.created_at)}</Code></Field>
        <Field label="dataset"><Code block>{inst.dataset}</Code></Field>
        <Field label="socket"><Code block>{inst.socket}</Code></Field>
        <Field label="subnet"><Code>{inst.subnet}</Code></Field>
        <Field label="metallb ip"><Code>{inst.metallb_ip}</Code></Field>
        <Field label="workload">
          {inst.workload_plugin ? (
            <Code>{inst.workload_plugin}</Code>
          ) : (
            <span class="text-text-muted text-sm">none (plain docker)</span>
          )}
        </Field>
        <Field label="clone of">
          {inst.clone_of ? <Code block>{inst.clone_of}</Code> : <span class="text-text-muted text-sm">—</span>}
        </Field>
      </div>
    </Panel>
  );
}

function SnapshotsTab({
  inst,
  onRevert,
  onClone,
}: {
  inst: Instance;
  onRevert: (label: string) => void;
  onClone: (label: string) => void;
}) {
  if (inst.snapshots.length === 0) {
    return (
      <Panel>
        <div class="px-4 py-10 text-center space-y-2">
          <p class="text-sm text-text">No snapshots yet.</p>
          <pre class="inline-block text-left text-sm font-mono bg-code-bg border border-border rounded-sm px-3 py-2 text-text">
            dockersnap snapshot {inst.name} golden
          </pre>
        </div>
      </Panel>
    );
  }
  return (
    <Panel class="overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="text-left text-xs uppercase tracking-wide text-text-muted font-mono border-b border-border">
            <th class="px-3 py-2 font-normal">label</th>
            <th class="px-3 py-2 font-normal">created</th>
            <th class="px-3 py-2 font-normal">tags</th>
            <th class="px-3 py-2 font-normal text-right">actions</th>
          </tr>
        </thead>
        <tbody class="[&_tr:nth-child(even)]:bg-surface-subtle">
          {inst.snapshots.map((s) => (
            <tr key={s.label} class="hover:bg-surface-hover">
              <td class="px-3 py-1.5"><Code>{s.label}</Code></td>
              <td class="px-3 py-1.5 font-mono text-text-secondary tabular-nums text-xs">
                {formatTime(s.created_at)}
              </td>
              <td class="px-3 py-1.5 text-xs space-x-1">
                {s.tags && Object.keys(s.tags).length > 0 ? (
                  Object.entries(s.tags).map(([k, v]) => (
                    <span key={k} class="font-mono bg-surface-hover px-1.5 py-0.5 rounded-sm text-text-secondary">
                      {k}={v}
                    </span>
                  ))
                ) : (
                  <span class="text-text-muted">—</span>
                )}
              </td>
              <td class="px-3 py-1.5 text-right">
                <button
                  type="button"
                  onClick={() => onRevert(s.label)}
                  class="text-xs text-accent hover:text-accent-hover font-mono mr-3"
                >
                  revert
                </button>
                <button
                  type="button"
                  onClick={() => onClone(s.label)}
                  class="text-xs text-accent hover:text-accent-hover font-mono"
                >
                  clone
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  );
}

// Hairline-bordered button used for the lifecycle row (Start / Stop /
// Restart). Distinct from the amber primary action and the red
// destructive — these are run-of-the-mill transitions, not the
// emphasis-grabbing operations.
function SecondaryButton({
  onClick,
  children,
}: {
  onClick: () => void;
  children: preact.ComponentChildren;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      class="border border-border text-text-secondary hover:border-border-strong hover:text-text px-3 py-1.5 rounded-sm text-sm font-medium"
    >
      {children}
    </button>
  );
}
