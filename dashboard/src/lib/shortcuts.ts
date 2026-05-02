import { useEffect } from "preact/hooks";

// Global keyboard shortcut wiring. `?` toggles the help overlay,
// individual pages register their own (e.g. `c` to open Create) by
// passing a map of key → handler. Bindings are ignored while the user
// is typing in an input/textarea/contenteditable so they don't fire
// while filling forms.
export function useShortcuts(map: Record<string, () => void>) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        target instanceof HTMLSelectElement ||
        target?.isContentEditable
      ) {
        return;
      }
      const handler = map[e.key];
      if (handler) {
        e.preventDefault();
        handler();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [Object.keys(map).join("|"), Object.values(map)]);
}
