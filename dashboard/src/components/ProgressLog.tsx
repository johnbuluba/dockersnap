import type { ProgressEvent, StreamStatus } from "../lib/stream";

// Live-progress panel — renders one row per NDJSON event from the daemon.
// Mirrors the CLI's →/✓/✗ prefix vocabulary so the UX is consistent.
const STATUS_GLYPH: Record<string, string> = {
  running: "→",
  done: "✓",
  error: "✗",
};

const STATUS_COLOR: Record<string, string> = {
  running: "text-accent",
  done: "text-status-running",
  error: "text-status-error",
};

export function ProgressLog({
  events,
  status,
  error,
}: {
  events: ProgressEvent[];
  status: StreamStatus;
  error: string | null;
}) {
  return (
    <div class="space-y-2">
      <div class="rounded-sm border border-border bg-code-bg max-h-64 overflow-y-auto font-mono text-xs">
        {events.length === 0 && status === "pending" && (
          <p class="px-3 py-3 text-text-muted">starting…</p>
        )}
        {events.length === 0 && status === "idle" && (
          <p class="px-3 py-3 text-text-muted">no events yet</p>
        )}
        {events.map((ev, i) => (
          <div
            key={i}
            class="px-3 py-1 flex items-start gap-2 border-b border-border/40 last:border-b-0"
          >
            <span class={`shrink-0 ${STATUS_COLOR[ev.status] ?? "text-text-muted"}`}>
              {STATUS_GLYPH[ev.status] ?? "·"}
            </span>
            <span class="text-text-secondary">{ev.step}</span>
            {ev.message && (
              <span class="text-text-muted truncate">— {ev.message}</span>
            )}
          </div>
        ))}
      </div>
      {error && (
        <div class="rounded-sm border border-status-error bg-status-error-bg px-3 py-2 text-sm">
          <span class="font-mono text-status-error">error</span>
          <span class="text-text ml-2 break-words">{error}</span>
        </div>
      )}
    </div>
  );
}
