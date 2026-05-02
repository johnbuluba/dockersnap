# Workload Plugin System — Design Document

**Status:** spec, not yet implemented
**Reference plugin:** `dockersnap-plugin-kind` (will live at `plugins/kind/` once implemented)

The project is in heavy development and pre-production — there's no installed-base compatibility to preserve, and every API/state change can be a single coordinated PR rather than a multi-phase migration. The deferred-from-v2 list at the end of this doc captures items earlier drafts speculated about; they get added when an actual workload demands them.

## 1. Overview

Decouple workload-specific logic (kind, compose, k3s, etc.) from the dockersnap core binary. The core manages **isolated Docker environment + ZFS dataset + network namespace**. Plugins handle workload deployment, access configuration, and health checks for whatever runs on top.

**Hard requirement:** the entire CLI must work both locally and against a remote daemon (`DOCKERSNAP_REMOTE=http://other-host:9847`). This rules out anything that requires the CLI to fork a binary on the same machine as the daemon — every plugin operation goes through the daemon's REST API as a normal JSON call.

## 2. Motivation

- **Separation of concerns:** core shouldn't know what kind is.
- **Extensibility:** add new workload types without rebuilding dockersnap.
- **Independent release cycles:** plugin updates don't require core redeployment.
- **Testability:** plugins are standalone binaries, testable in isolation.
- **Language freedom:** plugins can be written in Go, bash, Python — whatever fits. The SDK is Go-only; non-Go plugins implement the contract by hand.

## 3. Architecture

### 3.1 Exec-based plugins

```
/usr/local/lib/dockersnap/plugins/
  kind           # dockersnap-plugin-kind binary
  compose        # dockersnap-plugin-compose (future)
  k3s            # dockersnap-plugin-k3s (future)
```

Dockersnap discovers plugins at startup by scanning the plugins directory and running each one's `schema` and `init` commands. Each plugin is an executable that implements eight lifecycle commands.

### 3.2 Why exec-based

| Alternative | Rejected because |
|---|---|
| Go plugin `.so` | ABI breaks between Go versions; must compile with exact same toolchain. |
| gRPC/socket | Overkill; requires plugin to be a long-running daemon. |
| Embedded interface | Couples plugin code to core; rebuild for every change. |
| Scripting (Lua/JS) | Extra runtime dependency; limited ecosystem. |

Same pattern as CNI plugins, kubectl plugins, Git hooks.

### 3.3 Locality

Plugins run on the daemon host — always. The core invokes them via `os/exec`. Every plugin operation reaches the user via the REST API (JSON or NDJSON), so a CLI on a different machine sees the same behavior as a local CLI. There is **no** mode where the CLI invokes a plugin binary directly.

## 4. Plugin Contract

### 4.1 The eight commands

| Command | When | stdin | stdout | Exit |
|---|---|---|---|---|
| `init` | daemon startup | none | none (logs on stderr) | 0 = ready, 1 = unusable |
| `schema` | daemon startup | none | `SchemaResponse` JSON | 0 |
| `validate` | before `Manager.Create` | `PluginInput` | `ValidateResponse` JSON | 0 = valid, 1 = invalid |
| `deploy` | during `Manager.Create` | `PluginInput` | NDJSON progress, terminal `complete` event | 0 = success |
| `teardown` | during `Manager.Delete` | `PluginInput` | NDJSON progress | 0 = success |
| `access` | `dockersnap use`, `GET /api/v1/instances/{name}/access` | `PluginInput` | `AccessResponse` JSON | 0 |
| `describe` | `dockersnap workload describe`, `GET /api/v1/instances/{name}/workload` | `PluginInput` | `DescribeResponse` JSON | 0 |
| `health` | periodic poll, on demand | `PluginInput` | `HealthResponse` JSON | 0 = probes ran, 1 = plugin couldn't run probes |

That's the entire contract surface. No exec passthrough, no top-level aliases, no snapshot/revert hooks — those are deferred until something asks for them.

### 4.2 Contract version

Plugins declare the contract version they implement in `schema`:

```json
{ "contract_version": "1", "supported_contract_versions": ["1"] }
```

Core picks the highest mutually-supported version. Every other response also carries `"contract_version"`. Mismatched plugins are logged and disabled.

### 4.3 PluginInput (stdin for all commands except `init` and `schema`)

```json
{
  "contract_version": "1",
  "command": "deploy",
  "instance_name": "demo",
  "instance": {
    "name": "demo",
    "socket": "/run/dockersnap/demo.sock",
    "subnet": "10.10.0.0/16",
    "data_root": "/dockersnap/instances/demo",
    "host_veth_ip": "10.10.0.1",
    "ns_veth_ip": "10.10.0.2",
    "netns_name": "ds-demo",
    "metallb_ip": "10.10.10.10",
    "created_at": "2026-04-30T10:00:00Z",
    "clone_of": ""
  },
  "forwarded_ports": [
    { "label": "kubernetes-api", "container_port": 6443, "host_port": 34567, "protocol": "tcp" }
  ],
  "env": {
    "HTTP_PROXY":  "http://proxy.example.com:8080",
    "HTTPS_PROXY": "http://proxy.example.com:8080",
    "NO_PROXY":    "localhost,127.0.0.1,10.0.0.0/8"
  },
  "plugin_config": {
    "kind_config":  "/opt/demo/files/kind.yaml",
    "cluster_name": "demo",
    "retries":      3
  }
}
```

- `forwarded_ports` is populated for `access`, `describe`, `health` (where ports already exist). Empty for `validate` (instance not created yet) and `deploy` (containers not started yet).
- `env` is the explicit env-var passthrough (proxy + custom). Plugins also inherit the daemon's process env, but `env` is the contract surface for plugin authors.
- `plugin_config` has already been type-checked by core against the plugin's `schema.config_options` and had `{{ instance_name }}` substituted. Semantic validation (file existence, version compat) is the plugin's `validate` job.

### 4.4 Lifecycle integration with Manager

Where plugin hooks land relative to today's three-phase Create/Clone and `runStoppedZFSOp` flows:

| Manager op | Plugin hook | Insertion point | Failure semantics |
|---|---|---|---|
| `Create` | `validate` | before any ZFS work, in phase 1 | fail-fast; reservation rolled back |
| `Create` | `deploy` | after dockerd Start, before phase-3 status flip | failure → run `teardown` (best-effort) → destroy dataset → roll back state |
| `Delete` | `teardown` | before dockerd Stop | failure → log warning, continue (we're nuking the dataset anyway) |
| `Snapshot` | none | n/a | dockerd-stop is sufficient quiescing; ZFS captures clean state |
| `Revert` | none | n/a | same |
| `Clone` | none | n/a | source ports get randomized at file-system level (existing logic), no plugin hook needed |
| `Stop` / `Start` | none | n/a | live-restore handles it. **Plugins must set a Docker restart policy** (`--restart unless-stopped` or similar) on any container they spawn, otherwise it stays Exited after a `Stop` → `Start` cycle. |

Snapshot/revert/clone hooks are explicitly deferred. If a future workload needs to quiesce or rotate identity, we add the hooks then — design v2 spec'd them but no current need justifies the implementation cost.

### 4.5 Standard responses

#### `SchemaResponse`

```json
{
  "contract_version": "1",
  "supported_contract_versions": ["1"],
  "plugin_name": "kind",
  "plugin_version": "1.0.0",
  "description": "Deploy and manage kind (Kubernetes IN Docker) clusters",
  "config_options": [
    { "name": "cluster_name",       "type": "string", "default": "{{ instance_name }}", "required": false },
    { "name": "kind_config",        "type": "path",   "required": false,
      "description": "Path to a kind cluster config YAML" },
    { "name": "wait_ready",         "type": "bool",   "default": true,  "required": false },
    { "name": "retries",            "type": "int",    "default": 3,     "required": false },
    { "name": "kubernetes_version", "type": "string", "required": false }
  ]
}
```

Types: `string`, `int`, `bool`, `string-list`, `path` (string with file-existence check). Dotted names allowed for nested keys (`proxy.http`).

#### `ValidateResponse`

```json
{
  "contract_version": "1",
  "valid": false,
  "errors":   ["kind_config: file /opt/demo/files/kind.yaml does not exist"],
  "warnings": ["kubernetes_version not specified, will use kind default"]
}
```

Core runs schema-based type-checking before invoking `validate`. The plugin's `validate` is for semantic checks core can't do.

#### Progress events (for `deploy` / `teardown`)

NDJSON on stdout, one event per line, matching `instance.ProgressEvent`:

```
{"step":"checking_prereqs","status":"running","message":"Verifying kind binary"}
{"step":"checking_prereqs","status":"done"}
{"step":"creating_cluster","status":"running","message":"Pulling node image"}
{"step":"creating_cluster","status":"done"}
{"step":"complete","status":"done","message":"kind cluster 'demo' created"}
```

Core forwards plugin progress to API clients verbatim — no translation.

#### `AccessResponse`

The daemon injects `DOCKER_HOST` and `DOCKERSNAP_INSTANCE` into the `env`
map of every access response — they're derivable from instance state, so
the daemon owns them as a single source of truth. **Plugins should not
emit either key**; values returned for those keys get overwritten
silently by `injectDaemonEnv` in `internal/api/server.go`. Plugin `env`
should only contain genuinely plugin-specific entries (`KUBECONFIG`,
`ECHO_URL`, etc.).

```json
{
  "contract_version": "1",
  "env": {
    "KUBECONFIG":  "${ACCESS_DIR}/kubeconfig"
  },
  "files": [
    {
      "name": "kubeconfig",
      "content": "apiVersion: v1\nclusters:\n- cluster:\n    server: https://${HOST}:${PORT:kubernetes-api}\n    insecure-skip-tls-verify: true\n  name: kind-demo\n...",
      "mode": "0600"
    }
  ],
  "endpoints": [
    {
      "name": "kubernetes-api",
      "scheme": "https",
      "host_port_label": "kubernetes-api",
      "insecure": true,
      "description": "kubectl-compatible Kubernetes API"
    }
  ]
}
```

##### Token substitution

| Token | Resolved by | To |
|---|---|---|
| `${HOST}` | core | request Host header (or `cfg.API.ProxyBind` when it's an explicit non-`0.0.0.0`) |
| `${PORT:<label>}` | core | host port from `forwarded_ports` whose label matches |
| `${ACCESS_DIR}` | CLI | `~/.dockersnap/<instance>/` (left as a placeholder in the API response so different clients can choose different paths) |

Substitution is a single string replace pass. No expressions, no fall-throughs, no template engine.

##### End-to-end flow

```
user: eval $(dockersnap use demo)
  │
  ▼
CLI ── GET /api/v1/instances/demo/access ──▶ Core
                                              │
                                              ├─ load instance state, get workload_plugin="kind"
                                              ├─ build PluginInput (incl. forwarded_ports)
                                              ▼
                                            Plugin: dockersnap-plugin-kind access
                                              │
                                              ├─ DOCKER_HOST=unix://… kind get kubeconfig
                                              ├─ patch server URL → ${HOST}:${PORT:kubernetes-api}
                                              ├─ strip certificate-authority-data,
                                              │   add insecure-skip-tls-verify
                                              └─ emit AccessResponse JSON on stdout
                                              ▼
                                            Core
                                              │
                                              ├─ resolve ${HOST} from request Host header
                                              ├─ resolve ${PORT:kubernetes-api} from forwarded_ports
                                              └─ leave ${ACCESS_DIR} unresolved (CLI does it)
                                              ▼
CLI receives AccessResponse
  │
  ├─ mkdir -p ~/.dockersnap/demo/
  ├─ for each file: substitute ${ACCESS_DIR}, write with mode
  ├─ for each env var: substitute ${ACCESS_DIR}, print export
  └─ print endpoints to stderr for the human
```

This works identically against a local or remote daemon. `${HOST}` is whatever address the user typed to reach the daemon, so the kubeconfig points to that address.

#### `DescribeResponse`

```json
{
  "contract_version": "1",
  "workload_type": "kind",
  "status": "ready",
  "ports": [
    { "label": "kubernetes-api", "container_port": 6443, "protocol": "tcp" }
  ],
  "config":  { "cluster_name": "demo", "kubernetes_version": "v1.30.8" },
  "details": { "node_count": 1, "pod_subnet": "10.244.0.0/16" }
}
```

`config` is the resolved (post-template, post-validation) effective config. `details` is freeform metadata.

#### `HealthResponse`

The body's `healthy` field is the source of truth for workload health. Exit codes carry an orthogonal signal:

- **Exit 0** — the plugin successfully evaluated health. `healthy: false` is a normal case (the workload reports as unhealthy), and `checks[]` carries diagnostics. The daemon reads the body and surfaces it.
- **Exit 1** — the plugin itself couldn't run the probes (e.g. the kubeconfig file disappeared, an internal error). The daemon treats this as a plugin-level fault, distinct from "workload says it's unhealthy".

```json
{
  "contract_version": "1",
  "healthy": true,
  "checks": [
    { "name": "api-server",  "ok": true,  "message": "responding" },
    { "name": "nodes-ready", "ok": true,  "message": "1/1 nodes Ready" }
  ]
}
```

Core polls `health` every 30 seconds (configurable) and caches the result. `GET /api/v1/instances/{name}/health` returns the cache; `?fresh=true` forces a synchronous re-check. Unhealthy ≠ instance error — `Instance.Status` stays `running` because the dataset and dockerd are fine; health is workload-level.

#### Error response

Any command that exits non-zero may write a JSON error envelope to stdout:

```json
{ "contract_version": "1", "error": "kind create cluster failed", "details": "..." }
```

If stdout is empty, core uses captured stderr as the error message.

#### Plugin logs (stderr NDJSON)

Plugins log via stderr. Each line is a `LogEntry` JSON record:

```json
{"ts":"2026-05-02T...","level":"INFO","plugin":"kind","instance":"foo","command":"deploy","msg":"creating kind cluster","attrs":{"cluster":"foo","retries":3}}
```

The daemon's plugin runner parses every stderr line and re-emits it into the daemon's slog at the matching level, preserving `plugin` / `instance` / `command` and any free-form `attrs`. Lines that don't parse as a `LogEntry` (panics, bare `fmt.Println`, etc.) are logged verbatim at INFO so a misbehaving plugin still surfaces.

Plugin authors don't construct `LogEntry` directly — the SDK exposes `pluginsdk.NewLogger(p)` returning a `*slog.Logger` with the plugin name baked in, and `Context.Logger` is a per-invocation logger with the instance name auto-attached. Use it like any `slog.Logger`:

```go
func deployHandler(ctx context.Context, in *pluginsdk.Context, p *pluginsdk.Progress) error {
    in.Logger.Info("creating cluster", "cluster", clusterName, "retries", retries)
    // ...
    in.Logger.Warn("retrying", "attempt", n, "prev_error", err.Error())
}
```

Plugins emit at all levels; the daemon's `log_level` setting decides what makes it to the journal.

### 4.6 Templating

Plugin config values support exactly one template variable: `{{ instance_name }}`, resolved by core to `state.Instance.Name` before the plugin sees it. Pure string substitution. Unknown tokens (`{{ foo }}`) are a config validation error at startup.

This is intentionally the minimum. We expand the variable list when an actual plugin needs more.

## 5. Configuration

### `/etc/dockersnap/config.yaml`

```yaml
plugins:
  dir: /usr/local/lib/dockersnap/plugins
  health_poll_interval: 30s         # 0 = disable polling, pull-only
  health_failure_threshold: 3
  timeouts:
    init:     10s
    schema:   5s
    validate: 30s
    deploy:   600s    # kind create can be slow behind a corp proxy
    teardown: 120s
    access:   10s
    describe: 10s
    health:   10s

```

There is no preset registry — plugin configs are passed inline at create time
via `--plugin` + `--config k=v` / `--config-file <path>`. Authors who want a
named bundle of config use a shell alias or check a YAML file into version
control and reference it with `--config-file`.

### Plugin discovery flow

1. On startup, scan `plugins.dir` for executables.
2. Run `<plugin> schema` (5s timeout) — register the plugin with its declared schema. Failure → log + disable.
3. Run `<plugin> init` (10s timeout). Failure → log + disable.

`dockersnap plugin reload` re-runs steps 1–3 without restarting the daemon. `dockersnap plugin list` shows discovered plugins, status, and schema digest.

User-supplied plugin config (from `--config k=v` / `--config-file`) is type-checked against the plugin's declared `schema.config_options` at Create time, before the plugin's `validate` runs.

## 6. CLI Integration

```bash
# Create with a plugin (config is inline; --config-file for richer YAML):
dockersnap create demo --plugin kind --config-file ./demo-kind.yaml

# Plain Docker (no workload, no plugin):
dockersnap create bench

# Lifecycle — automatic:
dockersnap delete demo         # → plugin teardown → ZFS destroy
dockersnap use demo            # → plugin access → write files, export env

# Standard workload queries (no plugin-specific commands):
dockersnap workload describe demo
dockersnap workload health    demo
dockersnap workload schema    kind     # describes the plugin (no instance needed)

# Generic file extraction from the access response — replaces kind-specific
# kubeconfig in core:
dockersnap access demo --file kubeconfig
dockersnap access demo -o json          # full structured response

# Plugin admin:
dockersnap plugin list
dockersnap plugin describe kind
dockersnap plugin reload
```

There is **no** `dockersnap exec`. Workload-specific verbs that genuinely need to run on the daemon host (e.g. `kind load docker-image`) are added as concrete API endpoints on a case-by-case basis when the need arises — not as a generalized exec subsystem.

## 7. Plugin SDK (`pkg/pluginsdk`)

A Go module shipped from this repo. Hides the JSON wire format, contract negotiation, and progress streaming so plugin authors write business logic only.

Non-Go plugins implement the contract by hand against this design doc; the SDK is a convenience, not a requirement.

### 7.1 One-screen plugin

```go
package main

import (
    "context"
    "github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

func main() {
    p := pluginsdk.New(pluginsdk.Plugin{
        Name:                       "kind",
        Version:                    "1.0.0",
        Description:                "Deploy and manage kind clusters",
        SupportedContractVersions:  []string{"1"},
        ConfigOptions: []pluginsdk.ConfigOption{
            { Name: "cluster_name",       Type: pluginsdk.TypeString, Default: "{{ instance_name }}" },
            { Name: "kind_config",        Type: pluginsdk.TypePath },
            { Name: "wait_ready",         Type: pluginsdk.TypeBool, Default: true },
            { Name: "retries",            Type: pluginsdk.TypeInt,  Default: 3 },
            { Name: "kubernetes_version", Type: pluginsdk.TypeString },
        },
    })

    p.OnInit(initHandler)
    p.OnValidate(validateHandler)
    p.OnDeploy(deployHandler)
    p.OnTeardown(teardownHandler)
    p.OnAccess(accessHandler)
    p.OnDescribe(describeHandler)
    p.OnHealth(healthHandler)

    p.Run() // parses os.Args, dispatches, exits with the right code
}
```

### 7.2 Handler signatures

```go
type InitHandler     func(ctx context.Context) error
type ValidateHandler func(ctx context.Context, in *Context) (warnings []string, err error)
type DeployHandler   func(ctx context.Context, in *Context, p *Progress) error
type TeardownHandler func(ctx context.Context, in *Context, p *Progress) error
type AccessHandler   func(ctx context.Context, in *Context) (*AccessResponse, error)
type DescribeHandler func(ctx context.Context, in *Context) (*DescribeResponse, error)
type HealthHandler   func(ctx context.Context, in *Context) (*HealthResponse, error)
```

`Context` is the parsed `PluginInput`:

```go
type Context struct {
    InstanceName    string
    Instance        Instance        // typed copy of state.Instance fields
    ForwardedPorts  []ForwardedPort
    Env             map[string]string
    Config          Config          // typed accessor — see below
}

type ForwardedPort struct {
    Label         string
    ContainerPort int
    HostPort      int
    Protocol      string
}
```

### 7.3 Typed config access

```go
func deployHandler(ctx context.Context, in *Context, p *Progress) error {
    name       := in.Config.String("cluster_name")
    kindConfig := in.Config.Path("kind_config")    // file existence already verified
    waitReady  := in.Config.Bool("wait_ready")
    retries    := in.Config.Int("retries")
    k8sVersion := in.Config.OptString("kubernetes_version")
    // ...
}
```

`String`/`Bool`/`Int`/`Path` panic if the key isn't in `ConfigOptions` — that's a plugin-author bug, caught in tests. `OptString` returns `("", false)` for empty values.

### 7.4 Progress

```go
func deployHandler(ctx context.Context, in *Context, p *Progress) error {
    p.Step("checking_prereqs", "Verifying kind binary")
    if _, err := exec.LookPath("kind"); err != nil {
        return p.Fail("checking_prereqs", err)
    }
    p.Done("checking_prereqs")

    p.Step("creating_cluster", "Running kind create cluster")
    if err := runKind(ctx, in); err != nil {
        return p.Fail("creating_cluster", err)
    }
    p.Done("creating_cluster")

    p.Complete("kind cluster %q created", in.Instance.Name)
    return nil
}
```

`Step`/`Done`/`Fail`/`Complete` emit matching `instance.ProgressEvent` NDJSON. Handlers that don't emit progress are fine — SDK emits `complete` automatically before exit.

### 7.5 SDK helpers

```go
// Run a command inside the instance's network namespace (for things that
// need to reach 127.0.0.1:<port> bindings inside the netns).
out, err := pluginsdk.NsenterCommand(ctx, in.Instance.NetnsName,
    "kubectl", "--kubeconfig", path, "get", "nodes")

// Pre-built docker client targeting the instance's socket.
docker := pluginsdk.DockerClient(in.Instance.Socket)
containers, err := docker.ContainerList(ctx, container.ListOptions{})

// AccessResponse construction — token literals are SDK constants.
return &pluginsdk.AccessResponse{
    Env: map[string]string{
        "KUBECONFIG": pluginsdk.AccessDir + "/kubeconfig",
    },
    Files: []pluginsdk.File{
        pluginsdk.FileFromString("kubeconfig", patchedKubeconfig, 0600),
    },
    Endpoints: []pluginsdk.Endpoint{
        {Name: "kubernetes-api", Scheme: "https",
         HostPortLabel: "kubernetes-api", Insecure: true,
         Description: "kubectl-compatible Kubernetes API"},
    },
}, nil
```

### 7.6 Test harness

```go
// pkg/pluginsdk/sdktest

func TestDeploy_HappyPath(t *testing.T) {
    in := sdktest.NewContext(t).
        WithInstanceName("test").
        WithConfig(map[string]any{
            "cluster_name": "test", "wait_ready": true, "retries": 3,
        }).
        WithFakeKind(sdktest.KindOK()).
        Build()

    progress := sdktest.NewProgress()
    err := deployHandler(t.Context(), in, progress)
    require.NoError(t, err)
    assert.Contains(t, progress.Steps(), "creating_cluster")
}
```

`sdktest.NewContext()` returns a fully constructed `*Context`. `WithFakeKind` substitutes scriptable fakes for shell-outs.

### 7.7 SDK versioning

The SDK lives at `github.com/johnbuluba/dockersnap/pkg/pluginsdk` and is versioned independently from core via Go module semver. SDK v1.x.y maps to plugin contract version `"1"`. Breaking contract changes bump SDK major version and contract version simultaneously.

## 8. State Persistence

`state.Instance` gains three new fields:

```json
{
  "name": "demo",
  "status": "running",
  "dataset": "dockersnap/instances/demo",
  "subnet": "10.10.0.0/16",
  "socket": "/run/dockersnap/demo.sock",
  "workload_plugin": "kind",
  "workload_config": {
    "kind_config":  "/opt/demo/files/kind.yaml",
    "cluster_name": "demo",
    "retries":      3
  },
  "workload_contract_version": "1"
}
```

**No-workload case**: `workload_plugin == ""` means "plain Docker environment, no plugin". `Create` with no `--plugin` flag produces this. All plugin invocations (`validate`/`deploy`/`teardown`/`access`/`describe`/`health`) are skipped; `access` returns just `{env: {DOCKER_HOST: ...}}`.

**Immutability**: `workload_*` fields are written once on `Create` and never rewritten. `Revert` restores ZFS data without touching the workload binding. Migrating an instance to a different plugin or config is not supported in v1 — recreate the instance.

**Existing instances**: not a concern. The project is in heavy development and there's no production state to preserve. The plugin system lands as a single coordinated change: every existing instance is recreated under the new model.

## 9. API Integration

```
POST   /api/v1/instances                  # body: {name, workload_inline?: {plugin, config}}
GET    /api/v1/instances/{name}/access    # AccessResponse (token-resolved)
GET    /api/v1/instances/{name}/workload  # DescribeResponse
GET    /api/v1/instances/{name}/health    # HealthResponse, ?fresh=true forces re-check

GET    /api/v1/plugins                    # list discovered plugins
GET    /api/v1/plugins/{name}             # SchemaResponse
POST   /api/v1/plugins/reload             # re-scan plugins dir
```

All endpoints are normal JSON GET/POST. No streaming required for the plugin layer (deploy/teardown progress flows through the existing Snapshot-style NDJSON streaming on `POST /api/v1/instances` and `DELETE /api/v1/instances/{name}` when `Accept: application/x-ndjson` is set).

## 10. Security

- Plugins run as root (same uid as the daemon).
- Plugin binaries should be owned by root and not world-writable (daemon refuses to load looser perms).
- Config values are passed via JSON; no shell interpolation. Plugins must still avoid using values in `bash -c`-style invocations.
- Plugin stdio is captured by the daemon; secrets that must reach the API client (e.g. kubeconfig CA data) go through `AccessResponse`, not through stderr.

## 11. Implementation Order

Single coordinated change set, no migration scaffolding. The project is pre-production; we don't keep dual code paths.

1. **`internal/plugin/`** — exec runner, config type-checker, schema cache, plugin registry. Pure logic, fully unit-testable with a fake plugin binary.
2. **`pkg/pluginsdk/` + `pkg/pluginsdk/sdktest/`** — public SDK and in-process test harness.
3. **State file additions** — `workload_plugin`, `workload_config`, `workload_contract_version` on `state.Instance`. Existing on-disk state is wiped; instances must be recreated.
4. **Lifecycle integration** — wire plugin hooks into `Manager.Create` (validate + deploy), `Manager.Delete` (teardown), and the periodic health-poll goroutine in `cmd/serve.go`.
5. **API endpoints** — `/access`, `/workload`, `/health`, `/plugins`, `/plugins/{name}`, `/plugins/reload`. Swap `getKubeconfig` to delegate to the plugin's `access` (and remove the embedded `kind get kubeconfig` code path entirely).
6. **CLI updates** — `--plugin <name>` / `--config k=v` / `--config-file <path>` on `create`; `dockersnap workload {describe,health}` verbs; `dockersnap plugin {list,describe,reload}` verbs; `dockersnap access <inst> [--file <name>]` replaces the kind-specific `dockersnap kubeconfig`.
7. **`plugins/kind/`** — the reference plugin, written against the SDK. Replaces every embedded kind reference in core.
8. **Ansible role** — deploys `dockersnap-plugin-kind` to `/usr/local/lib/dockersnap/plugins/kind`.
9. **Docs** — update `AGENTS.md` and `docs/DESIGN.md` with the plugin contract.
10. **Tests** — unit tests for the plugin runner with a fake binary; integration tests using `client.WorkloadInline{Plugin: "kind"}`; SDK `sdktest`-based tests for the kind plugin.

## 12. Future Plugins

| Plugin | What it does |
|---|---|
| `kind` | Deploy/manage kind clusters (primary use case) |
| `compose` | Run docker-compose stacks inside isolated environments |
| `k3s` | Lightweight k3s cluster |
| `images` | Pre-pull/cache a list of container images |
| `ansible` | Run an Ansible playbook inside the namespace post-deploy |

## 13. Deferred from v2 (tracked, not blocking v1)

| Item | Why deferred |
|---|---|
| Exec passthrough (`dockersnap exec <inst> <cmd>`) | Adds a streaming protocol over HTTP for TTY support, or breaks remote use. Most needs (`kubectl`, `docker`) are already solved by `dockersnap use` + native tools. Add concrete endpoints (`POST .../workload/load-image`) when a specific need arises. |
| Pre/post snapshot/revert hooks | Today's stop-dockerd-then-ZFS is enough for kind. No current workload needs quiescing. |
| Post-clone hook | Two parallel kind clusters work fine in separate netns with different ports. Identity rotation is speculative. |
| Idempotency declarations + auto-recovery | Failed deploy → instance in `error` state → operator deletes + retries. Simpler than auto-teardown logic. |
| Plugin allowlist + checksums | Root owns the plugins dir; that's the threat model. Add hardening if a security review demands it. |
| Top-level CLI aliases | `dockersnap workload describe demo` is one extra word vs `dockersnap describe demo`. Not worth the collision-detection complexity. |
| Plugin chains | Single plugin per instance in v1. Plugins that need composition (kind needing pre-pulled images) handle it internally. |
| Sandboxing beyond uid 0 | Out of scope; exec-based architecture preserves the option. |
| Multi-variable templating | Only `{{ instance_name }}` in v1. Expand when needed. |
