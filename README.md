# dockersnap

[![CI](https://github.com/johnbuluba/dockersnap/actions/workflows/ci.yml/badge.svg)](https://github.com/johnbuluba/dockersnap/actions/workflows/ci.yml)
[![Docs](https://github.com/johnbuluba/dockersnap/actions/workflows/docs.yml/badge.svg)](https://johnbuluba.github.io/dockersnap/)
[![Go Reference](https://pkg.go.dev/badge/github.com/johnbuluba/dockersnap.svg)](https://pkg.go.dev/github.com/johnbuluba/dockersnap/pkg/pluginsdk)
[![Go Report Card](https://goreportcard.com/badge/github.com/johnbuluba/dockersnap)](https://goreportcard.com/report/github.com/johnbuluba/dockersnap)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

> Instant snapshot, revert, and clone of Docker-based dev environments via ZFS.

`dockersnap` runs each environment inside an **instance** — an isolated Docker
daemon on its own ZFS dataset, in its own network namespace. Snapshot the
environment in milliseconds, revert in seconds, clone for parallel work that
shares blocks until they diverge. A pluggable workload layer handles
environment-specific deployment (kind clusters today; anything Docker-based
in principle).

Built for the case where deploying a working environment takes long enough
that "nuke and pave" hurts — kind clusters with hundreds of pods, layered
services, paid-for setup time. Capture the golden state once; spin up branches
in seconds.

## Highlights

- **Instant filesystem operations.** ZFS copy-on-write means a snapshot is a
  metadata change, a clone is near-zero disk until writes diverge, and a revert
  is an atomic rollback.
- **One Docker daemon per instance.** Each instance gets a fully isolated
  dockerd in its own systemd unit + network namespace, so containers, images,
  volumes, and ports never leak between environments.
- **Workload plugins.** A small exec-based plugin contract layers
  environment-specific deploy/teardown/access/health on top of the bare
  Docker environment. The reference `kind` plugin runs full Kubernetes
  clusters; an `echo` plugin demonstrates the SDK in <100 lines.
- **First-class remote use.** The daemon speaks a REST API over HTTP; the CLI
  and an embedded Preact dashboard both consume the same surface, so a
  developer's laptop can drive an environment running on a beefy VM.
- **Embedded dashboard.** Single-binary deploy still — the dashboard ships
  inside the daemon via `go:embed`, served at `/ui/`.

## Quick start

### Prerequisites

- Linux host with the ZFS kernel module + userspace tools
- A disk, partition, or file-backed vdev for the ZFS pool
- Go 1.22+ to build from source

### One-time host setup (Ansible)

```bash
cd deploy/
cp inventory.yml.template inventory.yml
$EDITOR inventory.yml         # set your target host
ansible-playbook playbook.yml
```

The role installs the binary, sets up the ZFS pool, configures the systemd
unit, and provisions kernel sysctls (inotify limits) needed for parallel kind
clusters.

### Driving it from your laptop

Point the CLI at the daemon:

```bash
export DOCKERSNAP_REMOTE=http://my-vm:9847
```

Create an instance running a kind cluster, source the resulting environment,
work in it, snapshot it, branch off it:

```bash
dockersnap create dev --plugin kind --config retries=5
eval $(dockersnap use dev)              # exports DOCKER_HOST, KUBECONFIG, …
kubectl get nodes                       # works against the cluster on the VM

# (do destructive testing)

dockersnap snapshot dev golden          # ~2 s
dockersnap revert dev golden            # ~5 s, environment back to golden

dockersnap clone dev golden bench       # parallel branch, fresh subnet
eval $(dockersnap use bench)
kubectl get nodes                       # different cluster, same starting point
```

Or open the dashboard in a browser at `http://my-vm:9847/ui/` and do the same
through the UI — every CLI verb has a streaming-progress modal.

## CLI reference

Run `dockersnap --help` for the full grouped list. The verbs are:

| Group | Command | What it does |
|---|---|---|
| **Lifecycle** | `create <name> [--plugin <p>]` | New instance ± a workload plugin |
| | `delete <name>` | Destroy instance + snapshots |
| | `start` / `stop` / `restart <name>` | Lifecycle of the instance's dockerd |
| **State** | `snapshot <name> <label>` | Capture point-in-time state (with optional `--tag k=v`) |
| | `revert <name> <label>` | Roll back to a snapshot (`--force` to drop newer ones) |
| | `clone <name> <label> <new>` | Branch a snapshot into a new instance |
| **Access** | `use <name>` | `eval`-able shell exports + materialized files |
| | `access <name>` | Inspect what the plugin exposes (`--file <name>` prints one) |
| | `docker <name> -- <args>` | `docker` against the instance's daemon |
| | `workload describe`/`health <name>` | Plugin metadata / cached health (`--fresh`) |
| **Admin** | `list` / `status <name>` | Inventory + per-instance detail |
| | `health` / `version` | Daemon-level overview |
| | `plugin list` / `describe` / `reload` | Plugin admin |
| | `serve` | Run the daemon |

Aliases: `ls` / `ps` for `list`, `rm` / `destroy` for `delete`, `snap` for
`snapshot`, `kc` for `kubeconfig`, `info` / `show` for `status`, `rollback`
for `revert`, `new` for `create`, `wl` for `workload`.

## Workload plugins

A plugin is a standalone executable in `/usr/local/lib/dockersnap/plugins/`
implementing eight subcommands (`init` / `schema` / `validate` / `deploy` /
`teardown` / `access` / `describe` / `health`). The contract is JSON over
stdin/stdout (NDJSON for streaming progress on `deploy` / `teardown`) — see
[docs/PLUGIN-DESIGN.md](docs/PLUGIN-DESIGN.md) for the full spec.

The Go SDK at `pkg/pluginsdk` is one dependency, ~50 lines of plugin code:

```go
func main() {
    p := pluginsdk.New(pluginsdk.Plugin{
        Name:    "echo",
        Version: version.String(),
        ConfigOptions: []pluginsdk.ConfigOption{
            {Name: "text", Type: pluginsdk.ConfigTypeString,
             Default: "hello from {{ instance_name }}"},
        },
    })
    p.OnDeploy(deployHandler)
    p.OnAccess(accessHandler)
    p.OnHealth(healthHandler)
    p.Run()
}
```

Reference plugins:
- `plugins/kind/` — full kind cluster deploy + kubeconfig + per-pod health probes.
- `plugins/echo/` — minimal example (single HTTP container) used for SDK testing.

## API

When `dockersnap serve` is running, a REST API is available at
`http://<host>:9847/api/v1/`. The dashboard at `/ui/` is served by the same
process. Endpoints:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health` | Daemon overview (no auth) |
| `GET`/`POST` | `/instances` | List / create |
| `GET`/`DELETE` | `/instances/{name}` | Get / delete |
| `POST` | `/instances/{name}/{start,stop,snapshot,revert,clone}` | Lifecycle |
| `GET` | `/instances/{name}/{access,workload,workload/health,ports}` | Plugin views |
| `POST` | `/instances/{name}/ports/refresh` | Re-scan host port forwards |
| `GET` / `POST` | `/plugins`, `/plugins/{name}`, `/plugins/reload` | Plugin admin |

Mutations (`create`, `delete`, snapshot/revert/clone, start/stop) accept
`Accept: application/x-ndjson` and stream progress events line-by-line; both
the CLI and the dashboard consume that to render live progress.

Set `api.token` in `/etc/dockersnap/config.yaml` to require
`Authorization: Bearer <token>` (or `DOCKERSNAP_TOKEN` env var on the CLI side).

## Project layout

```
.
├── cmd/                       # CLI entry points (cobra)
├── internal/
│   ├── api/                   # REST server (chi)
│   ├── client/                # Go HTTP client
│   ├── config/                # /etc/dockersnap/config.yaml
│   ├── dashboard/             # go:embed of the built SPA
│   ├── dockerd/               # systemd transient unit + netns mgmt
│   ├── instance/              # Manager: lifecycle orchestration
│   ├── plugin/                # plugin runner + log streaming
│   ├── proxy/                 # host→netns TCP proxy
│   ├── state/                 # JSON state file with atomic mutations
│   └── zfs/                   # zfs CLI wrappers
├── pkg/
│   ├── pluginsdk/             # public Go SDK for plugin authors
│   └── version/               # build version stamping
├── plugins/
│   ├── kind/                  # reference: kind clusters
│   └── echo/                  # reference: minimal HTTP echo
├── dashboard/                 # Preact + Vite + Tailwind v4 SPA
├── deploy/                    # Ansible role
├── tests/
│   ├── e2e/                   # core lifecycle E2E (real VM)
│   └── plugins/{kind,echo}/   # per-plugin integration suites
└── docs/                      # design docs
```

## Building

The repo uses [Task](https://taskfile.dev/) (`go install github.com/go-task/task/v3/cmd/task@latest`).

```bash
task                     # list everything
task build               # build the daemon (regenerates types + dashboard first)
task test                # unit tests
task test:race           # with -race
task release             # cross-compile linux/amd64
task deploy              # build + scp + restart on the configured VM

# Dashboard
task ui:dev              # Vite dev server, /api proxied to a running daemon
task ui:gen-types        # regen TS types from Go via tygo
task ui:typecheck        # tsc --noEmit

# Integration tests (run on the VM with sudo)
task e2e:run             # core lifecycle suite
task e2e:plugins:run     # every plugin suite, sequentially
task e2e:all             # core + every plugin
task e2e:plugins:kind    # one plugin only
```

`task build` chains `ui:gen-types` → `ui:build` → `ui:embed` → `go build`,
so a single command produces a binary with up-to-date TS types and the
latest dashboard bundle embedded.

## Testing

**Unit tests** (no root, no ZFS, fast):

```bash
task test          # ./...
task test:race     # with the race detector
task test:cover    # coverage summary
```

**Integration tests** (run on the VM with root, validate ZFS / netns /
iptables / systemd / dockerd / live kind clusters):

```bash
task e2e:all                # full matrix — core + every plugin suite
task e2e:run                # core lifecycle only
task e2e:plugins:kind       # the kind plugin suite
task e2e:plugins:echo       # the echo plugin suite
```

The core suite validates ZFS snapshot/rollback/clone semantics, network
namespace + iptables setup, systemd transient unit lifecycle, and CoW data
integrity. The plugin suites test the full deploy → snapshot → revert → clone
→ teardown cycle through real workloads.

## Documentation

Full docs site: **<https://johnbuluba.github.io/dockersnap/>**.

User-facing:

- [Getting Started](docs/getting-started.md) — install + 5-minute walkthrough
- [CLI Reference](docs/cli.md) — every command with examples
- [Authoring a Plugin](docs/plugins/authoring.md) — wrap your own workload
- [Troubleshooting](docs/troubleshooting.md) — common issues

Internals:

- [docs/DESIGN.md](docs/DESIGN.md) — Architecture, decisions, rationale
- [docs/SNAPSHOT-INTERNALS.md](docs/SNAPSHOT-INTERNALS.md) — ZFS / Docker interaction
- [docs/PLUGIN-DESIGN.md](docs/PLUGIN-DESIGN.md) — Workload plugin contract + SDK reference
- [docs/DASHBOARD-DESIGN.md](docs/DASHBOARD-DESIGN.md) — Dashboard architecture, palette, design language
- [docs/MCP-DESIGN.md](docs/MCP-DESIGN.md) — Future MCP server for LLM clients (parked design)

Contributing:

- [CONTRIBUTING.md](CONTRIBUTING.md) — how to build, test, and submit changes
- [AGENTS.md](AGENTS.md) — conventions for AI / human contributors

## License

Apache License 2.0. See [LICENSE](LICENSE) for the full text.
