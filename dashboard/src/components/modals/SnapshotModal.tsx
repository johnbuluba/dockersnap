import { useState } from "preact/hooks";
import { Modal, ModalFooter } from "../Modal";
import { ProgressLog } from "../ProgressLog";
import { useStreamingMutation } from "../../lib/stream";

// Snapshot a running instance. label + optional tag rows (k=v).
export function SnapshotModal({
  open,
  instanceName,
  onClose,
}: {
  open: boolean;
  instanceName: string;
  onClose: () => void;
}) {
  const [label, setLabel] = useState("");
  const [tagRows, setTagRows] = useState<Array<{ k: string; v: string }>>([]);

  const tags: Record<string, string> = {};
  for (const r of tagRows) {
    if (r.k.trim()) tags[r.k.trim()] = r.v;
  }

  const m = useStreamingMutation({
    method: "POST",
    path: `/api/v1/instances/${encodeURIComponent(instanceName)}/snapshot`,
    body: { label: label.trim(), tags: Object.keys(tags).length ? tags : undefined },
    invalidate: [["instances"], ["instances", instanceName]],
  });

  const close = () => {
    if (m.status === "pending") return;
    m.reset();
    setLabel("");
    setTagRows([]);
    onClose();
  };

  return (
    <Modal open={open} title={`Snapshot ${instanceName}`} onClose={close}>
      {m.status === "idle" && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!label.trim()) return;
            m.start();
          }}
          class="space-y-3"
        >
          <label class="block space-y-1">
            <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
              label
            </span>
            <input
              autoFocus
              type="text"
              value={label}
              onInput={(e) => setLabel((e.target as HTMLInputElement).value)}
              placeholder="golden"
              required
              class="w-full font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none"
            />
          </label>

          <div class="space-y-1">
            <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
              tags (optional)
            </span>
            {tagRows.map((row, i) => (
              <div key={i} class="flex items-center gap-2">
                <input
                  type="text"
                  value={row.k}
                  onInput={(e) =>
                    setTagRows((rows) =>
                      rows.map((r, idx) =>
                        idx === i ? { ...r, k: (e.target as HTMLInputElement).value } : r,
                      ),
                    )
                  }
                  placeholder="key"
                  class="font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none w-32"
                />
                <span class="text-text-muted">=</span>
                <input
                  type="text"
                  value={row.v}
                  onInput={(e) =>
                    setTagRows((rows) =>
                      rows.map((r, idx) =>
                        idx === i ? { ...r, v: (e.target as HTMLInputElement).value } : r,
                      ),
                    )
                  }
                  placeholder="value"
                  class="font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none flex-1"
                />
                <button
                  type="button"
                  onClick={() =>
                    setTagRows((rows) => rows.filter((_, idx) => idx !== i))
                  }
                  class="text-text-muted hover:text-text px-1 text-base leading-none"
                  aria-label="Remove tag"
                >
                  ×
                </button>
              </div>
            ))}
            <button
              type="button"
              onClick={() => setTagRows((rows) => [...rows, { k: "", v: "" }])}
              class="text-xs text-accent hover:text-accent-hover font-mono"
            >
              + add tag
            </button>
          </div>

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
              Snapshot
            </button>
          </ModalFooter>
        </form>
      )}

      {m.status !== "idle" && (
        <div class="space-y-3">
          <ProgressLog events={m.events} status={m.status} error={m.error} />
          <ModalFooter>
            {m.status === "done" && (
              <button
                type="button"
                onClick={close}
                class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
              >
                Close
              </button>
            )}
            {m.status === "error" && (
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
