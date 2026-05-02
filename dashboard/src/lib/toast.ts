// Tiny toast queue. No portal library, no animation framework — just a
// signal-style global list with a subscribe hook. Used by the global
// QueryClient error handler so any failed query/mutation surfaces in
// the corner without each page having to remember to render an
// ErrorPanel.

import { useEffect, useState } from "preact/hooks";

export type ToastKind = "error" | "info" | "success";

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

type Listener = (toasts: Toast[]) => void;

let toasts: Toast[] = [];
const listeners = new Set<Listener>();
let nextId = 1;

function emit() {
  for (const l of listeners) l(toasts);
}

export function pushToast(kind: ToastKind, message: string, ttl = 6000) {
  const id = nextId++;
  toasts = [...toasts, { id, kind, message }];
  emit();
  if (ttl > 0) {
    setTimeout(() => dismissToast(id), ttl);
  }
  return id;
}

export function dismissToast(id: number) {
  toasts = toasts.filter((t) => t.id !== id);
  emit();
}

export function useToasts(): Toast[] {
  const [snapshot, setSnapshot] = useState<Toast[]>(toasts);
  useEffect(() => {
    const listener: Listener = (next) => setSnapshot(next);
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }, []);
  return snapshot;
}
