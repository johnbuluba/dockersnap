# AGENTS.md — AI Agent Instructions for dockersnap

## Project Overview

**dockersnap** is a Go CLI/daemon that manages isolated Docker daemon instances on ZFS datasets, providing instant snapshot/revert/clone of fully-deployed Docker-based dev environments (e.g. kind clusters).

Each "instance" is a self-contained environment: its own ZFS dataset, its own Docker daemon, its own Docker network, and one workload (such as a kind cluster) inside it.

## Architecture Context

- **Runtime:** Single binary (`dockersnap`) with two modes: CLI commands (local or remote) and `serve` (API daemon).
- **Storage:** ZFS pool `dockersnap` with datasets under `dockersnap/instances/<name>`. Docker uses overlay2 on top of the ZFS mount (NOT Docker's ZFS storage driver).
- **Isolation:** One `dockerd` process per instance, listening on `/run/dockersnap/<name>.sock`. Each daemon runs with `live-restore: true` so containers survive service restarts.
- **Networking:** Each instance gets an auto-allocated /16 subnet from a configurable range.
- **Proxy:** Per-instance dockerd processes inherit proxy env vars from config (`docker.proxy` section).
- **Config:** `/etc/dockersnap/config.yaml` (written by Ansible during setup).
- **State:** `/var/lib/dockersnap/state.json` (tracks instances, their datasets, snapshots, and network allocations).
- **Daemon configs:** `/var/lib/dockersnap/daemon-configs/<name>.json` (per-instance dockerd daemon.json).

## Key Invariants (NEVER violate these)

1. **Stop-before-mutate:** NEVER rollback or snapshot a ZFS dataset while its dockerd is running. Always: stop all containers → stop dockerd → mutate ZFS → start dockerd.
2. **Recursive snapshots:** Always use `zfs snapshot -r` and `zfs rollback -r` to capture/restore all child datasets atomically.
3. **One dockerd per instance:** No sharing Docker daemons between instances. Full process isolation.
4. **State file is source of truth:** If the state file says an instance exists, the corresponding ZFS dataset and dockerd should exist. Reconcile discrepancies on daemon startup.
5. **Network determinism:** Subnet allocation is deterministic based on instance index (not hash) to avoid collisions and allow predictable addressing.
6. **Robust stop:** Always stop containers before stopping dockerd. After dockerd stops, clean up leftover mounts (netns, overlay) and orphan processes (containerd-shim) before touching the ZFS dataset.
7. **Network namespace isolation:** Each dockerd runs in its own Linux network namespace (`ds-<name>`) with a veth pair to the host. This gives fully isolated iptables, bridges, and routing. kubectl access requires running inside the namespace: `ip netns exec ds-<name> kubectl ...`.

## Workflow Rules (ALWAYS follow these)

1. **Keep docs up to date:** When making design decisions or discovering important behavior, update the relevant doc (`docs/DESIGN.md`, `docs/SNAPSHOT-INTERNALS.md`, or this file). Docs are not an afterthought — they are part of the deliverable.
2. **Commit whenever it makes sense:** Don't accumulate huge uncommitted changesets. Commit after each logical unit of work (feature, bugfix, refactor). Use descriptive multi-line commit messages with sign-off.
3. **Test before deploying:** Always `go build ./...` and `go test ./...` before deploying to the VM.
4. **Deploy pattern:** Build → scp → stop service → replace binary → start service. The service has reconciliation so instances auto-recover.
5. **Remote VM access:** SSH to your dockersnap host (set `DOCKERSNAP_VM_HOST=user@vm.example.com` for the deploy tasks). Use `sudo` for privileged ops. If you're behind a corporate proxy, set `HTTP(S)_PROXY` for external downloads. `sudo bash` is blocked — use `sudo sh -c` instead.
6. **Git no-pager:** ALWAYS use `git --no-pager` for all git commands (log, diff, show, branch, etc.) to avoid hanging on pager prompts.

## Git Commit Message Convention (IMPORTANT — zsh fix)

**NEVER use `git commit -m "multiline..."` in zsh.** Newlines get swallowed.

**ALWAYS write the message to a temp file and use `git commit -s -F /tmp/commit-msg.txt`.**

Pattern:
```bash
printf 'feat: subject line here\n\nBody paragraph explaining what and why.\n- bullet points for details\n' > /tmp/commit-msg.txt
git --no-pager commit -s -F /tmp/commit-msg.txt
```

The `-s` flag appends a `Signed-off-by:` trailer using your `git config user.name` / `user.email`.

## Code Conventions

- **Language:** Go 1.26+ (see `go.mod`).
- **CLI framework:** cobra. `rootCmd.SilenceUsage = true` — RunE errors print just the `Error:` line, not the full usage block.
- **API framework:** chi (lightweight, stdlib-compatible). Token middleware is mounted on a `r.Group(...)` so `/api/v1/health` stays public regardless of route order.
- **Error handling:** Wrap errors with `fmt.Errorf("operation: %w", err)`. Never swallow errors silently — even shell-outs (`sync`, `drop_caches`, `umount`) should at minimum log on failure.
- **Logging:** `log/slog` (structured logging). Use `slog.With("instance", name)` for context.
- **ZFS interaction:** Shell out to `zfs`/`zpool` commands via `os/exec`. No CGO ZFS bindings.
- **Docker interaction:** Official Docker SDK for Go API calls; `docker` CLI for ad-hoc operations and `docker events` streams.
- **File paths:** All system paths via helpers on `config.Config` (`SocketPath`, `PidFilePath`, `DatasetPath`, `MountPoint`).
- **Instance names:** Validated by `instance.ValidateName` (`^[a-z][a-z0-9-]{0,31}$`). All API/CLI entry points must validate before reaching the manager.
- **State mutations:** Always go through `state.Store.Update(fn)` / `state.Store.View(fn)` so the load-modify-save cycle is atomic. Never `Load()` → mutate → `Save()` directly outside tests.
- **Manager lifecycle ops** that take long-running I/O follow a three-phase pattern: reserve under lock → heavy I/O outside lock → commit/rollback under lock.
- **Snapshot/Revert** share `runStoppedZFSOp` — extend it (don't duplicate it) when adding new ZFS-while-stopped operations.
- **Status field:** `state.Status` typed string — use `state.StatusRunning` / `StatusStopped` / `StatusError`, never bare strings.
- **Workload plugins:** layered on top of an instance via `dockersnap create <name> --plugin <name> [--config k=v | --config-file <path>]`. Plugin binaries live in `/usr/local/lib/dockersnap/plugins/`. Daemon-side runner: `internal/plugin`. Public Go SDK for plugin authors: `pkg/pluginsdk`. Reference plugin: `plugins/kind/`. Contract: see `docs/PLUGIN-DESIGN.md`. Plugins run on the daemon host; the CLI reaches them via API endpoints (`/access`, `/workload`, `/workload/health`, `/plugins`, `/plugins/reload`) so behavior is identical for local and remote `DOCKERSNAP_REMOTE`.
- **Testing:** `github.com/stretchr/testify` (`assert` for soft checks, `require` for hard preconditions) — both unit tests and the integration suite under `tests/e2e/`. Table-driven where the input grid is the point. Mocks for `zfs.Commander` and `instance.DockerdManager`. Plugin tests use shell-script fakes in `t.TempDir()` (not exec mocking). Integration tests tagged `//go:build integration`; system-state probes (`zfsDatasetExists`, `netnsExists`, `iptablesHasComment`, …) live in `tests/e2e/helpers_test.go` and return bools so the test decides what to assert.
- **Test layout:** Every package has `_test.go` companions for its pure logic; HTTP layer (`internal/api`, `internal/client`) is exercised via `httptest.Server`. Code that shells out to `systemctl`/`iptables`/`docker`/`zfs` (e.g. `internal/dockerd`, `internal/proxy/scan.go`, `EventListener`) is validated only in the integration suite — don't try to unit-test those by mocking `exec.Command`.
- **Integration tests** are split: `tests/e2e/` covers core dockersnap (ZFS lifecycle, netns, iptables, systemd) and `tests/plugins/<name>/` is a per-plugin suite (one binary, one Go package). Each suite has its own `main_test.go` that calls `integrationutil.Bootstrap("<suite>")` for client + namespaced instance prefix. Run them remotely via `task e2e:run`, `task e2e:plugins:kind`, `task e2e:plugins:echo`, or `task e2e:plugins:run` for everything.

## Directory Structure

```
cmd/           → Cobra CLI command definitions. Thin wrappers that call into internal/.
internal/      → All business logic. Packages:
  api/         → REST API server (chi), handlers, middleware
  client/      → HTTP client wrapping the REST API for the CLI
  instance/    → Instance lifecycle (create, delete, list, start, stop)
  dockerd/     → Docker daemon process management (start/stop/health)
  zfs/         → ZFS operations (dataset, snapshot, rollback, clone, destroy)
  network/     → Subnet auto-allocation
  proxy/       → Host-side TCP port proxy + auto-discovery watchers
  config/      → Configuration loading from /etc/dockersnap/config.yaml
  state/       → Persistent state management (JSON file)
  plugin/      → Workload-plugin runner (discovers and invokes plugin binaries)
pkg/           → Public Go modules (importable by plugin authors).
  pluginsdk/   → Plugin SDK: types, runner helpers, typed config, progress, sdktest
deploy/        → Ansible role for one-time infrastructure setup
docs/          → Design documents (DESIGN.md, SNAPSHOT-INTERNALS.md, PLUGIN-DESIGN.md)
plugins/       → Reference plugin binaries (e.g. plugins/kind/) — once implemented
```

## What NOT to Do

- Do NOT use Docker's ZFS storage driver configuration. We manage ZFS datasets ourselves and point each dockerd's `--data-root` at a dataset mountpoint.
- Do NOT attempt live/hot snapshots. Always stop all containers AND dockerd first.
- Do NOT use `zfs rollback` without `-r` flag if intermediate snapshots might exist.
- Do NOT hardcode subnet ranges. Always read from config.
- Do NOT assume ZFS pool name. Always read from config.
- Do NOT bind the API to 0.0.0.0 by default. Default to 127.0.0.1 for security. Configurable.
- Do NOT stop dockerd without stopping containers first — orphan containerd-shim processes will hold the ZFS dataset busy.
- Do NOT use `kind export kubeconfig` for stdout output — use `kind get kubeconfig` instead.

## Testing Approach

- Unit tests: Mock the `zfs` and `dockerd` interfaces. Test business logic in `internal/instance/`.
- Integration tests: Require actual ZFS pool (created in CI or locally). Use `//go:build integration` tag.
- End-to-end: Shell script that runs create → snapshot → revert → verify.

## Ansible Role Context

The `deploy/` directory contains a standalone Ansible playbook + role for setting up the host:
- Installs ZFS kernel modules and userspace tools
- Creates the ZFS pool on a specified vdev
- Sets `zfs_arc_max` to configured value (default 5GB)
- Installs Docker CE (disables system-wide daemon)
- Installs runtime dependencies (socat, iproute2, iptables, util-linux/nsenter, procps)
- Installs the `dockersnap` binary
- Writes `/etc/dockersnap/config.yaml` (including proxy settings)
- Enables and starts the `dockersnap` systemd service

