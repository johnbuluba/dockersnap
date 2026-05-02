import { useToasts, dismissToast } from "../lib/toast";

const KIND_COLOR: Record<string, string> = {
  error: "border-status-error bg-status-error-bg",
  info: "border-border bg-surface",
  success: "border-status-running bg-status-running-bg",
};

const KIND_LABEL: Record<string, string> = {
  error: "error",
  info: "info",
  success: "ok",
};

const KIND_LABEL_COLOR: Record<string, string> = {
  error: "text-status-error",
  info: "text-text-secondary",
  success: "text-status-running",
};

// Bottom-right stack. No animation library — entries appear instantly,
// auto-dismiss after their TTL, manual dismiss via the × button.
export function ToastStack() {
  const toasts = useToasts();
  if (toasts.length === 0) return null;
  return (
    <div class="fixed bottom-3 right-3 z-50 flex flex-col gap-2 max-w-md">
      {toasts.map((t) => (
        <div
          key={t.id}
          class={`rounded-sm border px-3 py-2 text-sm flex items-start gap-3 ${KIND_COLOR[t.kind]}`}
        >
          <span
            class={`font-mono text-xs uppercase tracking-wide shrink-0 ${KIND_LABEL_COLOR[t.kind]}`}
          >
            {KIND_LABEL[t.kind]}
          </span>
          <span class="text-text break-words flex-1">{t.message}</span>
          <button
            type="button"
            onClick={() => dismissToast(t.id)}
            class="text-text-muted hover:text-text shrink-0 leading-none text-base"
            aria-label="Dismiss"
          >
            ×
          </button>
        </div>
      ))}
    </div>
  );
}
