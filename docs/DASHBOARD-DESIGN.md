# dockersnap Dashboard — Design

A web UI for managing dockersnap instances, snapshots, and workload plugins.
Internal-tool quality; not a public product.

## 1. Goals

- One pane of glass for the everyday flows: create / snapshot / revert / clone
  / delete instances, inspect workload health, copy a kubeconfig.
- Live progress for long operations (deploy, clone, revert) — the CLI already
  streams NDJSON; the UI consumes the same stream.
- Ships with the daemon — no separate service to deploy. The dockersnap binary
  serves `/ui/*` from an embedded asset bundle.
- Useful with zero configuration. Same auth surface as the API
  (`DOCKERSNAP_TOKEN` if set, otherwise open).

## 2. Non-goals

- **Multi-tenancy / RBAC.** Single-user tool; the API token already gates
  access at the daemon boundary.
- **Multi-daemon switching.** Out of scope for v1. The page targets the
  daemon that served it.
- **Mobile / tablet layouts.** Desktop-only target; responsive down to
  ~1024px is fine, beyond that is wasted effort.
- **Logs aggregation.** Daemon logs live in journalctl; we don't replicate
  that. We may surface plugin-level NDJSON logs (the ones the runner already
  re-emits) in a future stage, but not v1.
- **Metrics / charts.** No time-series store exists yet. Adding charts means
  adding a TSDB; defer until there's a clear need.
- **Frontend test infrastructure.** Backend tests cover the API; the SPA is
  thin enough that visual regressions are caught by use. Add Playwright only
  when a regression actually slips through.
- **i18n, theming, custom branding.** English, two color modes (light + dark
  via Tailwind class strategy), done.

## 3. Stack

(Versions verified against current upstream docs — Preact 10, Tailwind 4,
TanStack Query 5, wouter 3.)

| Layer | Choice | Why |
|---|---|---|
| Framework | **Preact 10** via `@preact/preset-vite` | React API surface at ~3 KB runtime. The preset auto-aliases `react`/`react-dom` → `preact/compat` so React-ecosystem libraries (lucide, react-hook-form) drop in unchanged |
| Build | **Vite** | Fast HMR, first-class TS, builds to `dist/` we feed to `go:embed` |
| Language | **TypeScript** | API response types generated from the daemon's Go structs (a small `task ui:gen-types` target later — simplest is a hand-written `api.ts` for now since the API surface is small) |
| Routing | **wouter 3** | 2 KB; supports `nest` for nested route trees and `useSearchParams` for the `?tab=…` pattern below — both used directly |
| Data fetching | **TanStack Query 5** | Polls list/status, deduplicates fetches, refetches on focus. We use `mutation.variables` + `submittedAt` for optimistic UI rather than manual `setQueryData` writes (the v5 idiomatic path) |
| Streaming | Custom `useStreamingMutation` hook on `fetch` + `ReadableStream` | TQ mutations don't natively consume streams, but the hook is ~40 lines: parse NDJSON line-by-line, push events to local state, invalidate queries on terminal `complete`/`error` event |
| Styling | **Tailwind v4** via `@tailwindcss/vite` | Tokens declared in CSS via `@theme { --color-… }`, no `tailwind.config.js`. Dark mode uses the `dark:` variant; we override the default `prefers-color-scheme` selector to a class strategy with `@variant dark (.dark &)` so a manual toggle works |
| Icons | **lucide-react** | Tree-shakable, works with Preact via the `react` → `preact/compat` alias |
| Forms | **react-hook-form** | The Create modal has the only non-trivial form (dynamic config rows derived from plugin schema) — keeps validation + state simple |

Total expected bundle target: ~150 KB gzipped after Vite production build.

### Why not server-rendered / HTMX?

The interactive bits (live deploy progress, snapshot revert with picker,
config-key form rows) get awkward fast under server templates. SPA + Query
gives us optimistic UI for free and natural NDJSON consumption. The "single
binary" property is preserved by `go:embed`, so we don't lose deploy
simplicity.

## 3b. Design Language

The dockersnap CLI is the primary interface; the dashboard is its
extension, not its replacement. That shapes the visual language:

- **Terminal-adjacent, not marketing-site.** No hero blocks, no
  gradients, no rounded-xl pill buttons. Sharp corners up to `rounded-sm`,
  flat surfaces, generous use of monospaced type for identifiers
  (instance names, plugin names, snapshot labels, sockets, dataset paths).
- **Density over whitespace.** This tool gets used by people who already
  ran `dockersnap ls`. Show them the same data in fewer pixels — narrow
  rows, single-pixel borders, table-driven instance list.
- **Status as small dots, not pills.** A 6-px circle in the row is enough;
  the column header says "status." Color carries semantics: `running` =
  emerald, `stopped` = amber, `error` = red, plus a neutral grey for
  inactive/unknown. One accent color (cyan-500) for interactive elements;
  no rainbow.
- **Numeric values monospaced and right-aligned.** Snapshot count, uptime,
  bytes — comparison at a glance.
- **Single top bar, no sidebar.** Four destinations (Overview, Instances,
  Plugins, plus a settings dropdown for token/theme); a sidebar is
  overkill and steals the horizontal space we'd rather spend on tables.
- **Empty states do work.** "No instances yet — `dockersnap create foo`"
  with the actual command shown verbatim, copy-on-click. Don't ship a
  cartoon octopus.
- **Dark mode is the default**, light is the alternate. Operator tools
  live in dark terminals; matching that is a small respect for the user's
  visual context.

Concretely: a Linear / Datadog-density vibe rather than a Vercel / Stripe
landing-page vibe.

### 3b.1 Palette

**Direction: Amber CRT meets Linear.** Warm-grey chrome that pulls away
from the cold default-AI-dashboard greys, with a single amber accent
that nods at terminal heritage without going phosphor-green cosplay.
Status colors stay on their own conceptual territory so a button never
gets confused for a state indicator.

Drop this block into `dashboard/src/index.css` next to `@import "tailwindcss"`.

```css
@theme {
  /* ── Mode-invariant: accent + status semantics ────────────────────── */
  --color-accent:        oklch(0.78 0.160 75);   /* amber */
  --color-accent-hover:  oklch(0.84 0.160 75);
  --color-accent-muted:  oklch(0.78 0.160 75 / 0.18);  /* nav active state */
  --color-accent-fg:     oklch(0.18 0.020 60);   /* text on amber */

  --color-focus-ring:    oklch(0.78 0.160 75 / 0.45);  /* halo, not fill */
  --color-selection:     oklch(0.78 0.160 75 / 0.30);  /* text highlight  */

  --color-status-running: oklch(0.72 0.130 155);  /* sage emerald */
  --color-status-stopped: oklch(0.62 0.020 240);  /* slate blue   */
  --color-status-error:   oklch(0.62 0.180 25);   /* terracotta   */
  --color-status-error-hover: oklch(0.55 0.180 25);
  --color-status-unknown: oklch(0.55 0.005 60);   /* warm grey    */
}

/* ── Dark mode (default) ──────────────────────────────────────────────── */
:root {
  --color-bg:             oklch(0.180 0.005 60);
  --color-surface:        oklch(0.220 0.005 60);
  --color-surface-subtle: oklch(0.205 0.005 60);
  --color-surface-hover:  oklch(0.255 0.005 60);
  --color-border:         oklch(0.300 0.005 60);
  --color-border-strong:  oklch(0.420 0.005 60);
  --color-text:           oklch(0.960 0.005 60);
  --color-text-secondary: oklch(0.780 0.005 60);
  --color-text-muted:     oklch(0.580 0.005 60);
  --color-code-bg:        oklch(0.140 0.005 60);

  --color-status-running-bg: oklch(0.32 0.05 155);
  --color-status-stopped-bg: oklch(0.30 0.02 240);
  --color-status-error-bg:   oklch(0.32 0.07 25);
  --color-status-unknown-bg: oklch(0.28 0.005 60);
}

/* ── Light mode (opt-in via .light on <html>) ─────────────────────────── */
.light {
  --color-bg:             oklch(0.985 0.003 60);
  --color-surface:        oklch(1.000 0.000 0);
  --color-surface-subtle: oklch(0.975 0.003 60);
  --color-surface-hover:  oklch(0.960 0.003 60);
  --color-border:         oklch(0.900 0.003 60);
  --color-border-strong:  oklch(0.780 0.003 60);
  --color-text:           oklch(0.200 0.010 60);
  --color-text-secondary: oklch(0.450 0.005 60);
  --color-text-muted:     oklch(0.620 0.005 60);
  --color-code-bg:        oklch(0.960 0.003 60);

  --color-status-running-bg: oklch(0.92 0.04 155);
  --color-status-stopped-bg: oklch(0.92 0.02 240);
  --color-status-error-bg:   oklch(0.93 0.05 25);
  --color-status-unknown-bg: oklch(0.94 0.003 60);
}

/* Dark is the default; .light is the variant we toggle. */
@variant light (.light &);
```

**Per-token rationale (terse):**

| Token | Why this hue / lightness |
|---|---|
| `accent` (amber, h=75) | Distinct from the Tailwind-blue tell. 75° lands between yellow and orange — warm-alive without playful. Carries terminal heritage (amber CRTs); same value works in both modes. |
| `status-running` (sage, h=155, c=0.13) | Cooler than default emerald, desaturated. Reads "all systems nominal," not Christmas. Lives in green space while accent lives in amber space — dot beside button never competes. |
| `status-stopped` (slate-blue, h=240) | Opposite hemisphere of the accent so "stopped" can never be misread as "interactive." Cool, low chroma — idle, deliberate, not broken. |
| `status-error` (terracotta, h=25) | Muted red-orange instead of stock red-500. Serious without the form-validation plastic look. Low chroma to live alongside the warm chrome. |
| `status-unknown` (warm grey) | Same hue family as the chrome. Quieter than stopped — stopped is a state, unknown is the absence of one. |
| `bg / surface / border` (warm grey, h=60, c=0.005) | Hue 60° matches the accent's family. Micro-warmth separates this palette from generic neutral-900 / zinc-950 dashboards. Linear does this with reds; we do it with yellows. |
| `surface-subtle` | Striped tables and read-only inline panels — one tick distinct from `surface` without reaching `surface-hover` (reserved for interaction). |
| `status-*-bg` | When we need the status as a badge fill rather than a dot. Same hue, much higher lightness, low chroma so the color sits behind text without shouting. |
| `accent-muted` | Active-nav underline — accent without the button-volume. |
| `focus-ring` / `selection` | Accent at low alpha so focus halos and text highlights feel like the rest of the palette, not bolted on. |

### 3b.2 Typography

Two faces, both self-hosted under `dashboard/public/fonts/` so the
dashboard works offline-first and no external CDN can break the page.

```css
@theme {
  --font-sans: "Geist", ui-sans-serif, system-ui, sans-serif;
  --font-mono: "JetBrains Mono", ui-monospace, "Menlo", "Consolas", monospace;
}
```

- **Geist** for UI text. Free (OFL), characterful without screaming
  "Vercel" the moment you read it; clean enough to disappear at small
  sizes (we run a lot of `text-xs` / `text-sm` in tables). Pairs cleanly
  with JetBrains Mono.
- **JetBrains Mono** for everything `font-mono`: instance names, dataset
  paths, snapshot labels, sockets, plugin names, command samples in
  empty states, code blocks. The most CRT-adjacent of the open mono
  faces; has good `0` / `O` / `l` / `1` disambiguation, which matters
  when an operator is reading IDs at a glance.

Identifier rule: anything the user could type in the CLI ships in
`font-mono` — against `bg-code-bg` when it's a standalone block, plain
when it's inline. Numeric columns get `tabular-nums` for at-a-glance
comparison.

## 4. Information Architecture

```
/ui
├── /                       Overview (default)
├── /instances              Instance list (filterable)
├── /instances/:name        Instance detail (tabbed)
│   ├── ?tab=overview       Status, subnet, MetalLB IP, socket, created
│   ├── ?tab=snapshots      Snapshot table + actions (revert, delete)
│   ├── ?tab=access         Plugin access bundle: env, files, endpoints
│   ├── ?tab=workload       Plugin describe + health
│   └── ?tab=ports          Forwarded ports table
└── /plugins                Plugin list + per-plugin schema view
```

### 4.1 Overview

- Daemon block: version, uptime, total/running instance counts, healthy
  vs unhealthy workload counts (already at `/api/v1/health`).
- Instance summary: top-5 instances by recency, with status pill + quick
  links.
- Plugin summary: count + reload button.

### 4.2 Instance list

Table columns: **name** (link) · status pill · workload plugin · subnet ·
snapshots · clone-of · created. Filters: status, has-workload, search-by-name.
Bulk actions deferred (single-user assumption — bulk delete is a footgun).

### 4.3 Instance detail (tabbed)

- **Overview tab**: card layout — identity, network, dataset, lifecycle
  buttons (start / stop / restart / delete).
- **Snapshots tab**: table (label · created · tags · clones), buttons:
  *Snapshot now* (modal), *Revert* (picker w/ force checkbox + warning),
  *Clone from snapshot* (modal). Delete-snapshot is a future endpoint.
- **Access tab**: read-only view of the plugin's access response, with
  copy buttons per file (kubeconfig especially) and per env var. Show
  resolved endpoint URLs.
- **Workload tab**: describe response (plugin metadata, free-form JSON
  rendered as a tree); health response with the diagnostic checks list and
  a *Refresh* button (`?fresh=true`).
- **Ports tab**: forwarded ports table; *Refresh* button hits
  `/ports/refresh`.

### 4.4 Plugins page

Table: name · status · version · contract · description. Click a plugin →
schema view: ConfigOptions table (name · type · default · description),
binary digest, schema digest, last-loaded timestamp. *Reload* button.

### 4.5 Modals

- **Create instance**: name field, optional plugin dropdown (from
  `/api/v1/plugins`), config rows that switch UI based on the schema's
  ConfigOption types (string → input, bool → checkbox, int → number,
  path → file picker hint, list → repeating input). YAML textarea as an
  escape hatch (matches `--config-file`). Streams progress.
- **Snapshot**: label + tag rows.
- **Revert**: snapshot picker, force checkbox with red warning that newer
  snapshots will be destroyed. Streams progress.
- **Clone**: source snapshot picker + new instance name. Streams progress.
- **Delete**: confirmation with the instance name typed in (CLI parked
  Theme C will mirror this).

### 4.6 Live progress UX

When a long-running mutation starts, the modal switches to a live-progress
panel: each NDJSON event becomes a row (`→` running, `✓` done, `✗` error).
Stream EOF + `complete` event → success, dismiss button. Error event →
keep the panel open, show the error, *Close* / *Retry* buttons.

## 5. Daemon Integration

```go
//go:embed all:dashboard/dist
var dashboardFS embed.FS

func mountDashboard(r chi.Router) {
    sub, _ := fs.Sub(dashboardFS, "dashboard/dist")
    r.Handle("/ui/*", http.StripPrefix("/ui/", spaHandler(sub)))
    r.Get("/", redirectTo("/ui/"))
}
```

`spaHandler` serves files when they exist on disk; falls back to `index.html`
for unknown paths so client-side routing works on refresh.

CORS: the SPA is same-origin, so no extra config. The token middleware
already covers `/api/v1/*`; the dashboard reads it from a cookie set by a
new `POST /api/v1/login` (just verifies and stores the existing token —
no new auth model). For tokenless setups (default), `/login` is a no-op.

## 6. Build / Dev Loop

- `dashboard/` is a sibling of `cmd/`, `internal/`, etc. Its own `package.json`,
  `vite.config.ts`, etc.
- `task ui:dev` — runs `vite` against the daemon at `localhost:9847` (Vite
  proxies `/api/*` to the daemon).
- `task ui:build` — runs `vite build`, output `dashboard/dist/`. The `embed`
  directive picks it up next `go build`.
- `task build` (existing) gains a dependency on `ui:build` so the daemon
  binary always ships the latest dashboard. Dev iteration uses `ui:dev`
  against a running daemon — no rebuild of Go for UI changes.

## 7. Stages

Each stage stands alone — you can stop after any of them and have something
useful.

### Stage 1 — Foundation (~1 day)

- Scaffold `dashboard/` with `npm init preact` (gives Vite + TS + the
  `@preact/preset-vite` preset that auto-aliases react→preact/compat).
- Add Tailwind v4: `npm i -D tailwindcss @tailwindcss/vite`, register
  `tailwindcss()` in `vite.config.ts`, single `@import "tailwindcss"` in
  `src/index.css`. Configure class-based dark mode:
  `@variant dark (.dark &)` in the same file.
- Add design tokens via `@theme { --color-accent: oklch(0.74 0.13 220); … }`
  (the cyan accent + status colors from §3b).
- Wire `wouter` with `<Router base="/ui">` so the dashboard lives under
  `/ui/*` regardless of where the daemon serves it.
- Embed `dashboard/dist/` in the daemon, serve at `/ui/*` with the
  index.html fallback for client-side routes.
- Add `task ui:dev` (Vite dev server with `/api` proxied to the daemon)
  and `task ui:build`; thread `ui:build` into `task build`.
- Skeleton layout: top bar (logo wordmark + nav links + token-state pill +
  theme toggle), `<Outlet />` area, no content yet.

**Done when:** `task build && bin/dockersnap serve` shows the empty
skeleton at `http://localhost:9847/ui/`, theme toggle persists in
localStorage, refreshing on `/ui/instances/foo` doesn't 404.

### Stage 2 — Read-only views (~2 days)

- Overview page (daemon health card + instance summary).
- Instance list with filters + status pills.
- Instance detail Overview tab.
- TanStack Query polls instance list every 5s; pauses when the tab is
  hidden.

**Done when:** the entire CLI's `list` + `status` flow is reachable in
the UI; no mutations.

### Stage 3 — Mutations with live progress (~2 days)

- Create / delete / snapshot / revert / clone modals.
- `useStreamingMutation` hook: kicks off `fetch(url, { headers: { Accept:
  'application/x-ndjson' }, body })`, reads `response.body` via
  `getReader()` + `TextDecoderStream`, splits on `\n`, exposes
  `events: ProgressEvent[]` and `status: 'pending' | 'done' | 'error'`
  to the modal. On terminal `complete` event, `queryClient.invalidateQueries({
  queryKey: ['instances'] })`.
- Optimistic UI uses TQ v5's `mutation.variables` + `submittedAt` rather
  than manual `setQueryData` calls — the modal renders the new instance
  in the list-skeleton row while the stream runs, and the real entry
  replaces it on invalidation.
- `delete` modal requires the user to type the instance name to confirm
  (mirrors the parked CLI Theme C behavior; same UX in both surfaces).

**Done when:** every CLI lifecycle verb works from the UI with live
progress; killing the stream mid-deploy leaves the daemon in a clean
state (the existing rollback paths handle this).

### Stage 4 — Workload tabs (~half day)

- Access tab (env + files + endpoints, copy buttons).
- Workload describe + health tabs.
- Ports tab (with refresh button).

**Done when:** full per-instance plugin view is in the UI.

### Stage 5 — Plugins page (~half day)

- Plugin list + per-plugin schema view.
- Reload button.

**Done when:** `dockersnap plugin {list,describe,reload}` are all reachable.

### Stage 6 — Polish (~1 day)

- Empty states for every list (instances, snapshots, ports, plugins).
- Error toasts (TanStack Query `onError` global handler).
- Keyboard shortcuts: `c` create, `/` focus search, `?` shortcut overlay.
- Persistent filters (URL query params, not localStorage — shareable links).
- Browser title sync (`dockersnap — <instance>`).

**Done when:** the dashboard feels like something you'd actually want to
leave open in a tab.

### Future (not stages — opportunistic)

- **Plugin logs viewer.** The runner already re-emits plugin NDJSON logs
  into the daemon's slog. Add `GET /api/v1/instances/{name}/logs?since=..`
  that tails them; UI streams it.
- **Multi-daemon switcher.** Top-bar dropdown of saved `DOCKERSNAP_REMOTE`
  endpoints. Worth doing only if the user actually accumulates daemons.
- **Snapshot diff/restore-only-files.** Future ZFS feature, depends on
  `internal/zfs` exposing a diff command first.

## 8. Open Questions

- **Token bootstrap UX.** When `api.token` is set, the SPA needs to know
  it. Lean toward a top-bar field with token persisted in localStorage
  + sent as the existing `Authorization: Bearer …` header on every
  request — one less endpoint, no cookie/session state, matches the
  CLI's `DOCKERSNAP_TOKEN` env-var pattern. Revisit if multi-user shows
  up.
- **Type generation.** The Go API responses (`state.Instance`,
  `client.PluginInfo`, etc.) need TS counterparts. Two cheap options:
  (a) hand-write `dashboard/src/types/api.ts` to match — surface is
  small (~10 types); (b) add `task ui:gen-types` running `tygo` against
  internal/state + internal/client. Start with (a); switch to (b) when
  the surface grows or types drift.
- **Where to source plugin schemas in forms.** The Create modal needs to
  render type-appropriate inputs from `ConfigOption.Type`. Decide whether
  to hardcode the type → component map in the SPA, or make the daemon
  emit hint fields (`ui_widget: "textarea"`) per option. Lean toward the
  former for v1 — keeps the contract minimal.
- **Optimistic UI vs server truth on streamed mutations.** TQ v5's
  recommendation is to lean on `mutation.variables` + the pending state
  rather than write to the cache. For NDJSON streams that's still the
  best fit: the modal is the source of truth for the in-flight op, and
  invalidation on terminal event refetches the canonical state. Document
  this as the pattern; don't mix in `setQueryData` writes.
