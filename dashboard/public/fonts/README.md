# Self-hosted fonts

Both faces are loaded from this directory by `src/index.css`. Drop the
woff2 files in here so the dashboard works offline-first and no external
CDN can break the page.

## Files expected

- `Geist-Regular.woff2`
- `Geist-Medium.woff2`
- `Geist-SemiBold.woff2`
- `JetBrainsMono-Regular.woff2`
- `JetBrainsMono-Medium.woff2`

## Where to get them

- **Geist** — https://github.com/vercel/geist-font/tree/main/packages/next/dist/fonts/geist-sans
  (OFL-licensed). Grab the woff2s.
- **JetBrains Mono** — https://www.jetbrains.com/lp/mono/ → Download
  (OFL-licensed). The repo at https://github.com/JetBrains/JetBrainsMono
  ships woff2s under `fonts/webfonts/`.

The UI falls back to `ui-sans-serif` / `ui-monospace` until the woff2s
land, so the dashboard is functional but visually "incomplete" without
them — fine for early stages, fix before any release.
