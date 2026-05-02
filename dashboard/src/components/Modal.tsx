import type { ComponentChildren } from "preact";
import { useEffect } from "preact/hooks";

// Minimal modal — no portal wrangling, no animation library, no a11y
// theatre. The dashboard is a single-user internal tool; we just need a
// dimmed backdrop, a panel, and an Escape handler.
export function Modal({
  open,
  title,
  onClose,
  children,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ComponentChildren;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      class="fixed inset-0 z-50 flex items-start justify-center pt-20 px-4 bg-bg/80 backdrop-blur-sm"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-label={title}
    >
      <div
        class="w-full max-w-xl rounded-sm border border-border-strong bg-surface shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div class="flex items-center justify-between px-4 py-2 border-b border-border">
          <h2 class="text-sm font-mono uppercase tracking-wide text-text-muted">
            {title}
          </h2>
          <button
            type="button"
            onClick={onClose}
            class="text-text-muted hover:text-text px-1 text-lg leading-none"
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <div class="px-4 py-3">{children}</div>
      </div>
    </div>
  );
}

// Standardized footer button row for all modals.
export function ModalFooter({ children }: { children: ComponentChildren }) {
  return (
    <div class="flex items-center justify-end gap-2 pt-3 mt-3 border-t border-border">
      {children}
    </div>
  );
}
