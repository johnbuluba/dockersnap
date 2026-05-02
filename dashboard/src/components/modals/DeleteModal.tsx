import { useState } from "preact/hooks";
import { Modal, ModalFooter } from "../Modal";
import { ProgressLog } from "../ProgressLog";
import { useStreamingMutation } from "../../lib/stream";
import { CopyableName } from "../CopyableName";

// Type-the-name-to-confirm pattern. Mirrors the parked CLI Theme C
// behavior — same UX in both surfaces. Streams progress.
export function DeleteModal({
  open,
  instanceName,
  onClose,
  onSuccess,
}: {
  open: boolean;
  instanceName: string;
  onClose: () => void;
  onSuccess?: () => void;
}) {
  const [typed, setTyped] = useState("");
  const m = useStreamingMutation({
    method: "DELETE",
    path: `/api/v1/instances/${encodeURIComponent(instanceName)}`,
    invalidate: [["instances"], ["health"]],
  });

  const close = () => {
    if (m.status === "pending") return;
    if (m.status === "done" && onSuccess) onSuccess();
    m.reset();
    setTyped("");
    onClose();
  };

  const matches = typed.trim() === instanceName;

  return (
    <Modal
      open={open}
      title={`Delete ${instanceName}`}
      onClose={close}
    >
      {m.status === "idle" && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (matches) m.start();
          }}
          class="space-y-3"
        >
          <div class="rounded-sm border border-status-error bg-status-error-bg px-3 py-2 text-sm">
            <p class="text-text">
              This destroys the ZFS dataset, every snapshot, and the workload.
              <span class="block mt-1 text-text-secondary">
                Type <CopyableName name={instanceName} /> to confirm.
              </span>
            </p>
          </div>
          <input
            autoFocus
            type="text"
            value={typed}
            onInput={(e) => setTyped((e.target as HTMLInputElement).value)}
            placeholder={instanceName}
            class="w-full font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none"
          />
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
              disabled={!matches}
              class="border border-status-error text-status-error hover:bg-status-error hover:text-white disabled:opacity-50 disabled:hover:bg-transparent disabled:hover:text-status-error disabled:cursor-not-allowed px-3 py-1.5 rounded-sm text-sm font-medium"
            >
              Delete
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
