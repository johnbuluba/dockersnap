# Contributing to dockersnap

Thanks for your interest. This is a young project, so the contribution
process is intentionally lightweight.

## Where things live

- **`cmd/`** — Cobra CLI commands. Thin wrappers; logic belongs in `internal/`.
- **`internal/`** — daemon-side logic (API, instance manager, ZFS, dockerd,
  plugin runner, state, networking, proxy).
- **`pkg/pluginsdk/`** — public Go SDK for plugin authors. Anything here is
  load-bearing for downstream plugins; treat it like a published API.
- **`plugins/`** — reference plugins (`echo`, `kind`).
- **`dashboard/`** — Preact + Vite + Tailwind UI; embedded into the daemon
  binary at build time.
- **`deploy/`** — Ansible role for one-time host setup.
- **`tests/e2e/`** — core integration suite (ZFS, netns, iptables, systemd).
- **`tests/plugins/<name>/`** — one integration suite per plugin.
- **`docs/`** — what you're reading. Authored as Markdown, published via
  MkDocs to GitHub Pages.

A more thorough orientation is in [`AGENTS.md`](AGENTS.md).

## Building

```bash
# Install the Task runner
go install github.com/go-task/task/v3/cmd/task@latest

task                       # list everything
task build                 # build the daemon (regenerates types + dashboard first)
task test                  # unit tests (no root, no ZFS)
task test:race             # with -race
task plugins:build         # build every plugins/<name>/
```

`task build` chains `ui:gen-types` → `ui:build` → `ui:embed` → `go build`,
so a single command produces a binary with up-to-date TypeScript types and
the latest dashboard bundle embedded.

## Tests

| Suite | Needs root / ZFS? | How |
|---|---|---|
| Unit (`go test ./...`) | no | `task test` / `task test:race` |
| Core integration (`tests/e2e/`) | yes | `task e2e:run` (runs on a configured VM) |
| Per-plugin integration (`tests/plugins/<name>/`) | yes | `task e2e:plugins:<name>` or `task e2e:plugins:run` |

Set `DOCKERSNAP_VM_HOST=user@vm.example.com` so the `task e2e:*` and
`task deploy` targets know where to ship the binary.

Don't try to unit-test code that shells out to `systemctl`/`iptables`/
`docker`/`zfs` by mocking `exec.Command` — exercise it in the integration
suite instead.

## Pull requests

1. **Fork and branch.** `feat/<short-name>`, `fix/<short-name>`, etc.
2. **Keep changes focused.** One logical change per PR. Refactors that touch
   many files but don't change behaviour are easier to review than
   feature + refactor in the same diff.
3. **Conventional Commits.** See [AGENTS.md](AGENTS.md#git-commit-message-convention).
   Format: `<type>(<scope>): <subject>`. Types: `feat`, `fix`, `docs`,
   `refactor`, `test`, `chore`, `perf`, `build`, `ci`, `style`, `revert`.
   Append `!` (or use a `BREAKING CHANGE:` footer) for breaking changes.
4. **Update docs in the same PR** if behaviour changes. `docs/`, the
   `README`, or `AGENTS.md` are all in scope.
5. **Run the unit suite** (`task test:race`) before pushing. The
   integration suite is slower; run the suites relevant to your change at
   minimum.
6. **No `Signed-off-by` trailers.** This project doesn't require DCO
   sign-off.

## Plugin authors

If you're writing a workload plugin against `pkg/pluginsdk`, you don't
need to vendor or fork dockersnap — just import the SDK. See
[Authoring a plugin](docs/plugins/authoring.md). Bug reports for the
SDK API are welcome; **do not** import anything from `internal/`,
those packages may break between releases.

## Reporting issues

For a bug:

- What you ran (CLI command + relevant config snippets).
- What you expected.
- What happened (CLI output, `journalctl -u dockersnap` if relevant,
  `dockersnap status <name>` if instance-specific).
- Versions: `dockersnap version`, kernel (`uname -r`), ZFS (`zfs version`).

For a security issue: please email rather than filing a public issue.

## License

By contributing, you agree your contributions are licensed under the
[Apache License 2.0](LICENSE).
