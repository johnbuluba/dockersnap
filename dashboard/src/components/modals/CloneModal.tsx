import { useState } from "preact/hooks";
import { Modal, ModalFooter } from "../Modal";
import { ProgressLog } from "../ProgressLog";
import { useStreamingMutation } from "../../lib/stream";
import { formatTime } from "../../lib/format";
import type { Snapshot } from "../../lib/types";

// Clone a snapshot into a new instance.
export function CloneModal({
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
  const [newName, setNewName] = useState("");

  const m = useStreamingMutation({
    method: "POST",
    path: `/api/v1/instances/${encodeURIComponent(instanceName)}/clone`,
    body: { label, new_name: newName.trim() },
    invalidate: [["instances"], ["health"]],
  });

  const close = () => {
    if (m.status === "pending") return;
    m.reset();
    setLabel(preselect ?? snapshots[0]?.label ?? "");
    setNewName("");
    onClose();
  };

  return (
    <Modal open={open} title={`Clone from ${instanceName}`} onClose={close}>
      {m.status === "idle" && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!label || !newName.trim()) return;
            m.start();
          }}
          class="space-y-3"
        >
          <label class="block space-y-1">
            <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
              source snapshot
            </span>
            <select
              value={label}
              onChange={(e) => setLabel((e.target as HTMLSelectElement).value)}
              class="w-full font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text"
            >
              {snapshots.map((s) => (
                <option key={s.label} value={s.label}>
                  {s.label} — {formatTime(s.created_at)}
                </option>
              ))}
            </select>
          </label>

          <label class="block space-y-1">
            <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
              new instance name
            </span>
            <input
              autoFocus
              type="text"
              value={newName}
              onInput={(e) => setNewName((e.target as HTMLInputElement).value)}
              placeholder="dev-clone"
              pattern="[a-z][a-z0-9-]*"
              maxLength={32}
              required
              class="w-full font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none"
            />
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
              class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
            >
              Clone
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
