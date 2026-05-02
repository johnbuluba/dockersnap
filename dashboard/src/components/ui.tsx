import type { ComponentChildren } from "preact";

// Shared layout primitives. Kept in a single file so component pages stay
// short and the design language is easy to audit in one place.

/** Page header: small title + secondary subtitle. */
export function PageHeader({
  title,
  subtitle,
  action,
}: {
  title: string;
  subtitle?: string;
  action?: ComponentChildren;
}) {
  return (
    <div class="flex items-end justify-between gap-4">
      <div class="space-y-0.5">
        <h1 class="text-base font-medium text-text">{title}</h1>
        {subtitle && <p class="text-sm text-text-secondary">{subtitle}</p>}
      </div>
      {action && <div>{action}</div>}
    </div>
  );
}

/** Bordered surface — the dashboard's default container. */
export function Panel({
  children,
  class: cls = "",
}: {
  children: ComponentChildren;
  class?: string;
}) {
  return (
    <div class={`rounded-sm border border-border bg-surface ${cls}`}>
      {children}
    </div>
  );
}

/** Empty-state block. Optional CLI command shown verbatim per §3b. */
export function EmptyState({
  message,
  hint,
  cli,
}: {
  message: string;
  hint?: string;
  cli?: string;
}) {
  return (
    <div class="px-4 py-10 text-center space-y-2">
      <p class="text-sm text-text">{message}</p>
      {hint && <p class="text-xs text-text-secondary">{hint}</p>}
      {cli && (
        <pre class="inline-block text-left text-sm font-mono bg-code-bg border border-border rounded-sm px-3 py-2 mt-2 text-text">
          {cli}
        </pre>
      )}
    </div>
  );
}

/** Inline identifier rendering. Pass `block` for a standalone code-style cell. */
export function Code({
  children,
  block = false,
}: {
  children: ComponentChildren;
  block?: boolean;
}) {
  if (block) {
    return (
      <code class="font-mono text-sm text-text bg-code-bg border border-border rounded-sm px-1.5 py-0.5">
        {children}
      </code>
    );
  }
  return <code class="font-mono text-sm text-text-secondary">{children}</code>;
}

/** A label + value pair for definition-list-style displays. */
export function Field({
  label,
  children,
}: {
  label: string;
  children: ComponentChildren;
}) {
  return (
    <div class="flex flex-col gap-0.5">
      <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
        {label}
      </span>
      <span class="text-sm">{children}</span>
    </div>
  );
}

/** Loading shimmer. Used between the first render and the first query result. */
export function Skeleton({ class: cls = "" }: { class?: string }) {
  return (
    <span
      class={`inline-block bg-surface-hover rounded-sm animate-pulse ${cls}`}
      aria-hidden="true"
    />
  );
}

/** Error block surfaced from React Query failures. */
export function ErrorPanel({ error }: { error: unknown }) {
  const msg = error instanceof Error ? error.message : String(error);
  return (
    <div class="rounded-sm border border-status-error bg-status-error-bg px-3 py-2 text-sm">
      <span class="font-mono text-status-error">error</span>
      <span class="text-text ml-2">{msg}</span>
    </div>
  );
}
