import { useState } from "preact/hooks";

// Tiny clipboard hook — no external library. The reset-to-idle timeout
// is short on purpose; the "copied!" state is just a confirmation flash.
export function useCopy() {
  const [copied, setCopied] = useState<string | null>(null);

  const copy = async (value: string, key: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(key);
      setTimeout(() => setCopied((c) => (c === key ? null : c)), 1200);
    } catch {
      // Older browsers / insecure context — fall back to a textarea hack.
      const ta = document.createElement("textarea");
      ta.value = value;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try {
        document.execCommand("copy");
        setCopied(key);
        setTimeout(() => setCopied((c) => (c === key ? null : c)), 1200);
      } finally {
        document.body.removeChild(ta);
      }
    }
  };

  return { copy, copied };
}
