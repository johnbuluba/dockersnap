// useStreamingMutation — long-running ops that emit NDJSON progress.
//
// Why this isn't a TanStack Query useMutation: TQ mutations expect a
// single Promise return value. Our daemon emits a stream of progress
// events, and the modal needs to render each one as it arrives. We hand-
// roll the state machine on top of `fetch` + `ReadableStream`. On
// terminal events (`complete` or `error`), we invalidate the relevant
// query keys so the lists / detail pages refetch from authoritative
// state.

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useState } from "preact/hooks";
import { getToken } from "./token";

export interface ProgressEvent {
  step: string;
  status: "running" | "done" | "error" | string;
  message?: string;
}

export type StreamStatus = "idle" | "pending" | "done" | "error";

export interface StreamingMutationState {
  status: StreamStatus;
  events: ProgressEvent[];
  error: string | null;
}

export interface StreamingMutationOptions {
  /** HTTP method, defaults to POST. */
  method?: "POST" | "DELETE";
  /** API path, e.g. "/api/v1/instances". */
  path: string;
  /** JSON body, if any. */
  body?: unknown;
  /**
   * Query keys to invalidate on terminal success. Pass either a static
   * array or a function that derives keys from the request body.
   */
  invalidate?: string[][];
}

export interface StreamingMutationResult extends StreamingMutationState {
  /** Kick off the request. Idempotent — second call resets state. */
  start: () => Promise<void>;
  /** Reset to idle without firing a request. */
  reset: () => void;
}

export function useStreamingMutation(
  opts: StreamingMutationOptions,
): StreamingMutationResult {
  const qc = useQueryClient();
  const [state, setState] = useState<StreamingMutationState>({
    status: "idle",
    events: [],
    error: null,
  });

  const reset = useCallback(() => {
    setState({ status: "idle", events: [], error: null });
  }, []);

  const start = useCallback(async () => {
    setState({ status: "pending", events: [], error: null });

    const headers = new Headers({
      Accept: "application/x-ndjson",
      "Content-Type": "application/json",
    });
    const token = getToken();
    if (token) headers.set("Authorization", `Bearer ${token}`);

    let res: Response;
    try {
      res = await fetch(opts.path, {
        method: opts.method ?? "POST",
        headers,
        body: opts.body !== undefined ? JSON.stringify(opts.body) : null,
      });
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setState({ status: "error", events: [], error: msg });
      return;
    }

    if (!res.ok || !res.body) {
      const text = await res.text().catch(() => "");
      setState({
        status: "error",
        events: [],
        error: `${res.status} ${res.statusText}${text ? `: ${text}` : ""}`,
      });
      return;
    }

    // Daemon may answer either NDJSON (when stream was negotiated and the
    // op is async) or plain JSON (sync, no progress). If it's plain JSON
    // we treat it as a one-shot success.
    const ct = res.headers.get("Content-Type") ?? "";
    if (!ct.includes("application/x-ndjson")) {
      // Sync path. Invalidate and we're done.
      for (const key of opts.invalidate ?? []) {
        qc.invalidateQueries({ queryKey: key });
      }
      setState({ status: "done", events: [], error: null });
      return;
    }

    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
    let buffer = "";
    let terminalError: string | null = null;
    let collected: ProgressEvent[] = [];

    try {
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += value;
        // Split on \n, keep the last partial line for next iteration.
        let idx: number;
        while ((idx = buffer.indexOf("\n")) >= 0) {
          const line = buffer.slice(0, idx).trim();
          buffer = buffer.slice(idx + 1);
          if (!line) continue;
          let ev: ProgressEvent;
          try {
            ev = JSON.parse(line) as ProgressEvent;
          } catch {
            continue;
          }
          collected = [...collected, ev];
          if (ev.status === "error") {
            terminalError = ev.message ?? "operation failed";
          }
          // Push current accumulator into state so the modal renders.
          setState((s) => ({ ...s, events: collected }));
        }
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setState({ status: "error", events: collected, error: msg });
      return;
    }

    if (terminalError) {
      setState({ status: "error", events: collected, error: terminalError });
      return;
    }

    for (const key of opts.invalidate ?? []) {
      qc.invalidateQueries({ queryKey: key });
    }
    setState({ status: "done", events: collected, error: null });
  }, [opts.path, opts.method, JSON.stringify(opts.body ?? null), qc]);

  return { ...state, start, reset };
}
