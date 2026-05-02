import { useEffect, useState } from "preact/hooks";
import { useShortcuts } from "../lib/shortcuts";

// Small keyboard-shortcut overlay. Toggled by `?`. Static content —
// nothing per-page; pages with their own bindings document them inline.
const SHORTCUTS: Array<{ keys: string; label: string }> = [
  { keys: "?", label: "Toggle this overlay" },
  { keys: "c", label: "Create instance (on Instances page)" },
  { keys: "/", label: "Focus filter input (on Instances page)" },
  { keys: "Esc", label: "Close any open modal" },
];

export function HelpOverlay() {
  const [open, setOpen] = useState(false);
  useShortcuts({ "?": () => setOpen((v) => !v) });

  // Independent Esc handler so it works even when no modal is open.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  if (!open) return null;
  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-bg/80 backdrop-blur-sm"
      onClick={() => setOpen(false)}
    >
      <div
        class="w-full max-w-sm rounded-sm border border-border-strong bg-surface"
        onClick={(e) => e.stopPropagation()}
      >
        <div class="px-4 py-2 border-b border-border flex items-center justify-between">
          <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
            shortcuts
          </span>
          <button
            type="button"
            onClick={() => setOpen(false)}
            class="text-text-muted hover:text-text leading-none text-base"
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <ul class="px-4 py-3 space-y-1 text-sm">
          {SHORTCUTS.map((s) => (
            <li key={s.keys} class="flex items-center justify-between gap-3">
              <span class="text-text-secondary">{s.label}</span>
              <kbd class="font-mono text-xs bg-code-bg border border-border rounded-sm px-1.5 py-0.5 text-text">
                {s.keys}
              </kbd>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
