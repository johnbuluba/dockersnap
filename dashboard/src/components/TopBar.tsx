import { Link, useLocation } from "wouter-preact";
import { useTheme } from "../lib/theme";
import { useToken } from "../lib/token";
import { useState } from "preact/hooks";
import { Logo } from "./Logo";

// Single header strip: wordmark, primary nav, token state pill, theme toggle.
// No sidebar — the dashboard has four destinations and density wins.
export function TopBar() {
  const [theme, setTheme] = useTheme();
  const [token, setToken] = useToken();
  const [location] = useLocation();
  const [tokenOpen, setTokenOpen] = useState(false);

  return (
    <header class="border-b border-border bg-surface">
      <div class="flex items-center gap-6 px-4 h-11">
        {/* Wordmark: cascade mark + monospaced product name. The mark
            inherits currentColor so it themes against either mode; we set
            text-accent on the inline-flex wrapper so only the logo picks
            up amber, not the wordmark. */}
        <Link href="/" class="flex items-center gap-2 font-mono text-sm font-medium tracking-tight">
          <Logo class="size-5 text-accent shrink-0" />
          <span class="text-text">dockersnap</span>
        </Link>

        <nav class="flex items-center gap-1 text-sm">
          <NavLink href="/" current={location} label="Overview" />
          <NavLink href="/instances" current={location} label="Instances" />
          <NavLink href="/plugins" current={location} label="Plugins" />
        </nav>

        <div class="ml-auto flex items-center gap-3">
          {/* Token state pill — clickable to open the editor. */}
          <button
            type="button"
            onClick={() => setTokenOpen((v) => !v)}
            class="text-xs font-mono px-2 py-0.5 rounded-sm border border-border hover:border-border-strong text-text-secondary"
            title="Configure API token"
          >
            <span class={`inline-block size-1.5 rounded-full mr-1.5 align-middle ${token ? "bg-status-running" : "bg-status-unknown"}`} />
            {token ? "token: set" : "token: none"}
          </button>

          {/* Theme toggle — single button, no submenu. */}
          <button
            type="button"
            onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
            class="text-xs font-mono px-2 py-0.5 rounded-sm border border-border hover:border-border-strong text-text-secondary"
            aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} mode`}
          >
            {theme === "dark" ? "dark" : "light"}
          </button>
        </div>
      </div>

      {tokenOpen && <TokenEditor token={token} onSave={(t) => { setToken(t); setTokenOpen(false); }} onClose={() => setTokenOpen(false)} />}
    </header>
  );
}

function NavLink({ href, current, label }: { href: string; current: string; label: string }) {
  // Active when the path matches or starts with the link (so /instances/foo
  // highlights the Instances tab). The root link only matches exactly.
  const active = href === "/" ? current === "/" : current === href || current.startsWith(href + "/");
  return (
    <Link
      href={href}
      class={`px-2 py-1 rounded-sm transition-colors ${
        active
          ? "text-accent bg-accent-muted"
          : "text-text-secondary hover:text-text hover:bg-surface-hover"
      }`}
    >
      {label}
    </Link>
  );
}

function TokenEditor({
  token,
  onSave,
  onClose,
}: {
  token: string | null;
  onSave: (next: string | null) => void;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState(token ?? "");
  return (
    <div class="border-t border-border bg-surface-subtle px-4 py-3 flex items-center gap-2">
      <label class="text-xs text-text-secondary font-mono">DOCKERSNAP_TOKEN</label>
      <input
        type="password"
        autoFocus
        value={draft}
        onInput={(e) => setDraft((e.target as HTMLInputElement).value)}
        placeholder="paste token (leave empty to clear)"
        class="flex-1 max-w-md font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none"
      />
      <button
        type="button"
        onClick={() => onSave(draft.trim() ? draft.trim() : null)}
        class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1 rounded-sm text-sm font-medium"
      >
        Save
      </button>
      <button
        type="button"
        onClick={onClose}
        class="text-text-secondary hover:text-text px-2 py-1 rounded-sm text-sm"
      >
        Cancel
      </button>
    </div>
  );
}
