import { useRef, useState } from "preact/hooks";
import { useCopy } from "../lib/copy";

// Identifier rendered as inline `<code>`. Double-clicking the element
// selects the entire string and copies it to the clipboard, with a brief
// "copied" flash. Browser default double-click selects the *word* — which
// breaks on hyphens (e.g. "kind-1640376-td" gets only "kind"), so we
// override to select the whole thing.
//
// Used in confirmation modals (and anywhere else the user is expected to
// transcribe an identifier into an input). Plain Ctrl+C after double-click
// also works, in case the user prefers it over the auto-copy.
export function CopyableName({ name }: { name: string }) {
  const ref = useRef<HTMLElement>(null);
  const { copy, copied } = useCopy();
  const [flash, setFlash] = useState(false);

  const handleDouble = () => {
    const el = ref.current;
    if (el) {
      const sel = window.getSelection();
      if (sel) {
        const range = document.createRange();
        range.selectNodeContents(el);
        sel.removeAllRanges();
        sel.addRange(range);
      }
    }
    copy(name, "copyable:" + name);
    setFlash(true);
    setTimeout(() => setFlash(false), 1200);
  };

  return (
    <span class="inline-flex items-center gap-1 align-baseline">
      <code
        ref={ref}
        title="Double-click to copy"
        onDblClick={handleDouble}
        class="font-mono bg-code-bg border border-border rounded-sm px-1 select-all cursor-copy"
      >
        {name}
      </code>
      {flash && copied === "copyable:" + name && (
        <span class="text-xs font-mono text-status-running select-none">
          copied
        </span>
      )}
    </span>
  );
}
