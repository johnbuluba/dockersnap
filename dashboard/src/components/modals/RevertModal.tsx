import { useState } from "preact/hooks";
import { Modal, ModalFooter } from "../Modal";
import { ProgressLog } from "../ProgressLog";
import { useStreamingMutation } from "../../lib/stream";
import { formatTime } from "../../lib/format";
import type { Snapshot } from "../../lib/types";

// Revert to a snapshot. The picker is the snapshot list; --force has a
// red callout because it destroys newer snapshots silently.
export function RevertModal({
  open,
  instanceName,
  snapshots,
  preselect,
  onClose,
}: {
  open: boolean;
  instanceName: string;
  snapshots: Snapshot[];
  preselect?: string;
  onClose: () => void;
}) {
  const [label, setLabel] = useState(preselect ?? snapshots[0]?.label ?? "");
  const [force, setForce] = useState(false);

  // Index of the chosen snapshot in the chronological list. Anything newer
  // than this gets destroyed when force=true; we surface that count to the
  // user so the warning is concrete, not abstract.
  const sorted = [...snapshots].sort((a, b) =>
    a.created_at.localeCompare(b.created_at),
  );
  const idx = sorted.findIndex((s) => s.label === label);
  const newerCount = idx >= 0 ? sorted.length - 1 - idx : 0;

  const m = useStreamingMutation({
    method: "POST",
    path: `/api/v1/instances/${encodeURIComponent(instanceName)}/revert`,
    body: { label, force },
    invalidate: [["instances"], ["instances", instanceName]],
  });

  const close = () => {
    if (m.status === "pending") return;
    m.reset();
    setLabel(preselect ?? sorted[0]?.label ?? "");
    setForce(false);
    onClose();
  };

  return (
    <Modal open={open} title={`Revert ${instanceName}`} onClose={close}>
      {m.status === "idle" && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!label) return;
            m.start();
          }}
          class="space-y-3"
        >
          <label class="block space-y-1">
            <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
              snapshot
            </span>
            <select
              value={label}
              onChange={(e) => setLabel((e.target as HTMLSelectElement).value)}
              class="w-full font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text"
            >
              {sorted.map((s) => (
                <option key={s.label} value={s.label}>
                  {s.label} — {formatTime(s.created_at)}
                </option>
              ))}
            </select>
          </label>

          {newerCount > 0 && !force && (
            <div class="rounded-sm border border-status-stopped bg-status-stopped-bg px-3 py-2 text-sm">
              <p class="text-text">
                {newerCount} snapshot{newerCount === 1 ? "" : "s"} newer than{" "}
                <code class="font-mono">{label}</code> will block the revert.
              </p>
            </div>
          )}

          <label class="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              checked={force}
              onChange={(e) => setForce((e.target as HTMLInputElement).checked)}
              class="size-3.5 mt-1 accent-status-error"
            />
            <span>
              <span class="font-mono text-status-error text-xs uppercase tracking-wide">
                force
              </span>
              <p class="text-text-secondary text-xs mt-0.5">
                Also destroy newer snapshots. This cannot be undone.
              </p>
            </span>
          </label>

          <ModalFooter>
            <button
              type="button"
              onClick={close}
              class="text-text-secondary hover:text-text px-3 py-1.5 rounded-sm text-sm"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!label || (newerCount > 0 && !force)}
              class="bg-accent text-accent-fg hover:bg-accent-hover disabled:opacity-50 disabled:cursor-not-allowed px-3 py-1.5 rounded-sm text-sm font-medium"
            >
              Revert
            </button>
          </ModalFooter>
        </form>
      )}

      {m.status !== "idle" && (
        <div class="space-y-3">
          <ProgressLog events={m.events} status={m.status} error={m.error} />
          <ModalFooter>
            {m.status !== "pending" && (
              <button
                type="button"
                onClick={close}
                class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
              >
                Close
              </button>
            )}
            {m.status === "pending" && (
              <span class="text-xs text-text-muted font-mono">streaming…</span>
            )}
          </ModalFooter>
        </div>
      )}
    </Modal>
  );
}
