import { Link } from "wouter-preact";

export function NotFound() {
  return (
    <section class="rounded-sm border border-border bg-surface px-4 py-10 text-center">
      <p class="font-mono text-sm text-text-secondary">404 — no such route</p>
      <p class="text-xs text-text-muted mt-1">
        <Link href="/" class="text-accent hover:text-accent-hover">Back to overview</Link>
      </p>
    </section>
  );
}
