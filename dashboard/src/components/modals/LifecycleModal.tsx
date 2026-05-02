import { useEffect, useState } from "preact/hooks";
import { useQueryClient } from "@tanstack/react-query";
import { Modal, ModalFooter } from "../Modal";
import { ProgressLog } from "../ProgressLog";
import type { ProgressEvent, StreamStatus } from "../../lib/stream";
import { getToken } from "../../lib/token";

export type LifecycleAction = "start" | "stop" | "restart";

const LABEL: Record<LifecycleAction, string> = {
  start: "Start",
  stop: "Stop",
  restart: "Restart",
};

const DESCRIPTION: Record<LifecycleAction, string> = {
  start: "Start the dockerd for this instance.",
  stop: "Stop the instance's containers and dockerd.",
  restart:
    "Stop, then start. Existing containers come back up via dockerd live-restore.",
};

// Restart is two sequential streams (stop → start) shown in a single log.
// Start/Stop are one each. We don't reuse useStreamingMutation here
// because it's hard-coded for one-shot calls; the orchestration is a
// thin loop.
export function LifecycleModal({
  open,
  action,
  instanceName,
  onClose,
}: {
  open: boolean;
  action: LifecycleAction;
  instanceName: string;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [status, setStatus] = useState<StreamStatus>("idle");
  const [events, setEvents] = useState<ProgressEvent[]>([]);
  const [error, setError] = useState<string | null>(null);

  // Reset whenever the modal is reopened with the same/different action.
  useEffect(() => {
    if (!open) return;
    setStatus("idle");
    setEvents([]);
    setError(null);
  }, [open, action]);

  const close = () => {
    if (status === "pending") return;
    onClose();
  };

  const run = async () => {
    setStatus("pending");
    setEvents([]);
    setError(null);

    const phases: Array<"stop" | "start"> =
      action === "restart" ? ["stop", "start"] : [action];

    for (const phase of phases) {
      const res = await streamPhase(
        instanceName,
        phase,
        (ev) => setEvents((cur) => [...cur, ev]),
      );
      if (res.error) {
        setError(res.error);
        setStatus("error");
        // Still invalidate so the UI reflects partial state.
        qc.invalidateQueries({ queryKey: ["instances"] });
        qc.invalidateQueries({ queryKey: ["instances", instanceName] });
        return;
      }
    }

    qc.invalidateQueries({ queryKey: ["instances"] });
    qc.invalidateQueries({ queryKey: ["instances", instanceName] });
    qc.invalidateQueries({ queryKey: ["health"] });
    setStatus("done");
  };

  return (
    <Modal open={open} title={`${LABEL[action]} ${instanceName}`} onClose={close}>
      {status === "idle" && (
        <div class="space-y-3">
          <p class="text-sm text-text-secondary">{DESCRIPTION[action]}</p>
          <ModalFooter>
            <button
              type="button"
              onClick={close}
              class="text-text-secondary hover:text-text px-3 py-1.5 rounded-sm text-sm"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={run}
              class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
            >
              {LABEL[action]}
            </button>
          </ModalFooter>
        </div>
      )}

      {status !== "idle" && (
        <div class="space-y-3">
          <ProgressLog events={events} status={status} error={error} />
          <ModalFooter>
            {status !== "pending" && (
              <button
                type="button"
                onClick={close}
                class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
              >
                Close
              </button>
            )}
            {status === "pending" && (
              <span class="text-xs text-text-muted font-mono">streaming…</span>
            )}
          </ModalFooter>
        </div>
      )}
    </Modal>
  );
}

// streamPhase POSTs to /start or /stop with Accept: application/x-ndjson
// and pushes each line as a ProgressEvent. Returns { error } on terminal
// failure, or {} on success.
async function streamPhase(
  instanceName: string,
  phase: "start" | "stop",
  onEvent: (ev: ProgressEvent) => void,
): Promise<{ error?: string }> {
  const headers = new Headers({ Accept: "application/x-ndjson" });
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);

  let res: Response;
  try {
    res = await fetch(
      `/api/v1/instances/${encodeURIComponent(instanceName)}/${phase}`,
      { method: "POST", headers },
    );
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) };
  }
  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => "");
    return {
      error: `${res.status} ${res.statusText}${text ? `: ${text}` : ""}`,
    };
  }

  const ct = res.headers.get("Content-Type") ?? "";
  if (!ct.includes("application/x-ndjson")) {
    // Sync fallback — nothing to render, treat as a one-shot success.
    return {};
  }

  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  let terminalError: string | null = null;

  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += value;
      let idx: number;
      while ((idx = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, idx).trim();
        buffer = buffer.slice(idx + 1);
        if (!line) continue;
        try {
          const ev = JSON.parse(line) as ProgressEvent;
          onEvent(ev);
          if (ev.status === "error") {
            terminalError = ev.message ?? "operation failed";
          }
        } catch {
          // ignore malformed lines
        }
      }
    }
  } catch (err) {
    return { error: err instanceof Error ? err.message : String(err) };
  }

  return terminalError ? { error: terminalError } : {};
}
