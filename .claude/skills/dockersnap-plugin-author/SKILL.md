---
name: dockersnap-plugin-author
description: Use when writing, debugging, or modifying a dockersnap workload plugin in Go using pkg/pluginsdk — including new plugins under plugins/<name>/, edits to plugins/echo/ or plugins/kind/, or questions about the eight-command contract, plugin SDK, schema config, NDJSON progress streaming, plugin logging, container labelling for clones, restart-policy survival across stop/start, and access/health response shapes.
---

# Writing a dockersnap workload plugin

A workload plugin is a standalone Go binary that implements the eight-command
exec contract documented in `docs/PLUGIN-DESIGN.md`. The dockersnap daemon
finds plugins under `/usr/local/lib/dockersnap/plugins/<name>` and invokes
them with JSON on stdin / stdout. The Go SDK at `pkg/pluginsdk` handles the
boilerplate; a real plugin is ~50–200 lines.

This skill captures the **rules and gotchas** that aren't obvious from the
SDK alone. Read `docs/PLUGIN-DESIGN.md` for the full contract, and look at
`plugins/echo/` (minimal) and `plugins/kind/` (full-featured) as worked
examples.

## File layout

A new plugin lives under `plugins/<name>/`:

```
plugins/foo/
├── main.go         # New(Plugin{...}) + Run()
├── deploy.go       # OnDeploy handler
├── teardown.go     # OnTeardown handler
├── access.go       # OnAccess handler (often + helpers)
├── describe.go     # OnDescribe handler
├── health.go       # OnHealth handler
└── docker.go       # any plugin-shared helpers (talking to dockerd, etc.)
```

The build system (`plugins/Taskfile.yml`) auto-discovers any directory under
`plugins/` that contains a `main.go`. No registry edits needed — the binary
name comes from the directory name.

## The eight commands

| Command | When the daemon runs it | Stdin | Stdout | Exit |
|---|---|---|---|---|
| `init` | once at daemon startup | none | none (errors on stderr) | 0 = ready, 1 = unusable |
| `schema` | at daemon startup | none | `SchemaResponse` JSON | 0 |
| `validate` | before `Manager.Create` | `PluginInput` | `ValidateResponse` | 0 = valid, 1 = invalid |
| `deploy` | during `Manager.Create` | `PluginInput` | NDJSON progress + terminal `complete` | 0 = success |
| `teardown` | during `Manager.Delete` | `PluginInput` | NDJSON progress | 0 = success (best-effort failure tolerated) |
| `access` | `dockersnap use` / `GET /access` | `PluginInput` | `AccessResponse` | 0 |
| `describe` | `dockersnap workload describe` | `PluginInput` | `DescribeResponse` | 0 |
| `health` | periodic poll (30s) + on demand | `PluginInput` | `HealthResponse` | 0 = probes ran, 1 = couldn't probe |

The SDK's `Runner` dispatches subcommands to handlers you register via
`OnInit` / `OnValidate` / `OnDeploy` / etc. You don't write the JSON
plumbing — just handler functions.

## Skeleton

```go
package main

import (
    "github.com/johnbuluba/dockersnap/pkg/pluginsdk"
    "github.com/johnbuluba/dockersnap/pkg/version"
)

func main() {
    p := pluginsdk.New(pluginsdk.Plugin{
        Name:                      "foo",
        Version:                   version.String(),
        Description:               "What this plugin does, one line.",
        SupportedContractVersions: []string{"1"},
        ConfigOptions: []pluginsdk.ConfigOption{
            {Name: "image", Type: pluginsdk.ConfigTypeString,
             Default: "myorg/foo:latest", Description: "Container image."},
            {Name: "port", Type: pluginsdk.ConfigTypeInt,
             Default: 8080},
            {Name: "wait_ready", Type: pluginsdk.ConfigTypeBool,
             Default: true},
        },
    })
    p.OnDeploy(deployHandler)
    p.OnTeardown(teardownHandler)
    p.OnAccess(accessHandler)
    p.OnDescribe(describeHandler)
    p.OnHealth(healthHandler)
    p.Run() // parses os.Args, dispatches, exits with the right code
}
```

`OnInit` and `OnValidate` are optional. Skip a handler entirely and the
SDK emits a sensible default (init = success, deploy/teardown = no-op
with a `complete` event, etc.).

## Mandatory rules — get these wrong and things silently break

### 1. Set `--restart unless-stopped` on every container you spawn

**Why:** `dockersnap stop` triggers `systemctl stop dockersnap-<inst>` →
SIGTERM to dockerd → dockerd gracefully stops containers. With
`unless-stopped`, those containers come back when dockerd starts again.
Without a restart policy, they stay `Exited` and the workload is dark
after the next `dockersnap start`.

```go
runCmd := dockerCmd(ctx, in,
    "run", "--detach",
    "--restart", "unless-stopped",   // ← required
    "--label", labelPlugin+"=foo",
    image, "--port", strconv.Itoa(port),
)
```

`kind` containers do this themselves via `kind create cluster`, so the
kind plugin doesn't need to set it explicitly. Anything else does.

### 2. Filter container lookups by plugin label only — never by instance name

**Why:** ZFS clones copy the source's container labels verbatim,
including `dockersnap.instance=<source-name>`. A plugin that filters
`docker ps --filter label=dockersnap.instance=<this-instance-name>` will
miss the cloned container and report "no foo container found."

The plugin label filter is sufficient because every plugin invocation
already targets a specific instance's docker socket via
`-H unix://<inst>.sock` — the container can only belong to that instance.

```go
// Right:
cmd := dockerCmd(ctx, in,
    "ps", "--quiet",
    "--filter", "label=dockersnap.plugin=foo",
)

// Wrong — breaks on clones:
cmd := dockerCmd(ctx, in,
    "ps", "--quiet",
    "--filter", "label=dockersnap.plugin=foo",
    "--filter", "label=dockersnap.instance="+in.InstanceName,
)
```

The instance label is still useful to stamp on containers (visible in
`docker ps`); just don't use it as a match key.

### 3. Don't emit `DOCKER_HOST` or `DOCKERSNAP_INSTANCE` from `access`

**Why:** Both are derivable from instance state, so the daemon owns
them. Every `access` response gets these two env vars injected
unconditionally by `injectDaemonEnv` in `internal/api/server.go`,
overwriting any plugin-emitted values. Emitting them in the plugin
adds nothing and risks divergence (e.g. a plugin computing a stale
socket path).

```go
// Right — plugin only emits keys that are genuinely plugin-specific:
return &pluginsdk.AccessResponse{
    Env: map[string]string{
        "MY_TOOL_URL": "http://" + pluginsdk.HostToken + ":" + pluginsdk.PortToken("api"),
        "KUBECONFIG":  pluginsdk.AccessDirToken + "/kubeconfig",
    },
    // ...
}, nil

// Wrong — daemon overwrites these anyway:
Env: map[string]string{
    "DOCKER_HOST":         "unix://" + in.Instance.Socket,
    "DOCKERSNAP_INSTANCE": in.InstanceName,
    // ...
}
```

### 4. Health exit code: 0 means "probes ran"; 1 means "I couldn't probe"

The body's `healthy` field carries the verdict. Exit code is orthogonal —
exit 0 even when reporting `healthy: false`, as long as your probes ran
cleanly. Reserve exit 1 for "I couldn't connect to anything to check."

```go
func healthHandler(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.HealthResponse, error) {
    // Returning a non-nil response with healthy=false → SDK exits 0.
    return &pluginsdk.HealthResponse{
        Healthy: allChecksOK,
        Checks:  checks,
    }, nil

    // Returning err → SDK exits 1 (the daemon treats this as a plugin
    // fault, distinct from "the workload is unhealthy").
}
```

The daemon differentiates these: a `healthy: false` body shows the
diagnostic checks to the user; an exit-1 surfaces as a plugin-level
error in the API.

## Plugin SDK essentials

### `pluginsdk.Context` (passed to every handler)

| Field | What it is |
|---|---|
| `InstanceName` | The dockersnap instance the daemon is calling you for. |
| `Instance` | Network/socket details: `Socket`, `Subnet`, `NetnsName`, `MetalLBIP`, `HostVethIP`, `NsVethIP`, etc. |
| `ForwardedPorts` | Live host-side port forwards (populated for `access` / `describe` / `health`). Empty for `validate` / `deploy`. |
| `Env` | Daemon-injected env vars (proxy settings, etc.) for shelling out to `docker`/`kind`/etc. |
| `Config` | Typed accessor over your declared `ConfigOptions`. |
| `Logger` | `*slog.Logger` that emits NDJSON to stderr; the daemon re-emits into its log with `plugin=` / `instance=` / `command=` attrs. |

### `Config` accessors

`Config.String("name")`, `Config.Int(...)`, `Config.Bool(...)`,
`Config.Path(...)`, `Config.StringList(...)` — typed getters that fall
back to the schema's declared `Default`. Each panics if you ask for a
key with the wrong type tag (caught in unit tests).

`Config.OptString` / `OptPath` are present/absent variants — return
`(value, true)` when set, `("", false)` otherwise.

Type-tag-vs-accessor mismatches **panic at runtime**. Always match:
`ConfigTypeString` → `Config.String()`, `ConfigTypePath` → `Config.Path()`,
etc.

### Templating in defaults

Schema `Default` values are template-resolved by core before you see
them — the only supported token is `{{ instance_name }}`. So this works:

```go
{Name: "cluster_name", Type: pluginsdk.ConfigTypeString,
 Default: "{{ instance_name }}"},
```

Then `in.Config.String("cluster_name")` returns `<actual instance name>`.
This is core's job; don't re-implement template expansion in your handler.

### Logging

```go
in.Logger.Info("creating cluster", "cluster", name, "retries", retries)
in.Logger.Warn("retrying", "attempt", n, "prev_error", err.Error())
```

Logs route to the daemon's structured log automatically — you don't write
a logger setup. Plugin authors emit at all levels; the daemon's
`log_level` filters.

## AccessResponse — the user's connection bundle

Three sections: `Env` (shell exports), `Files` (materialized at
`~/.dockersnap/<inst>/<name>` by the CLI), `Endpoints` (URL-typed entries).

Token substitution lets you reference the user's host and the
forwarded-port labels without knowing their values:

| Token | Resolved by | Resolves to |
|---|---|---|
| `${HOST}` (`pluginsdk.HostToken`) | daemon | Whatever address the user is hitting the daemon on (or `api.proxy_bind` if set). |
| `${PORT:label}` (`pluginsdk.PortToken("label")`) | daemon | The host port currently mapped to that forward label. |
| `${ACCESS_DIR}` (`pluginsdk.AccessDirToken`) | CLI | The on-disk dir where files materialize, e.g. `~/.dockersnap/foo/`. |

The kind plugin's response uses all three (DOCKER_HOST is omitted —
the daemon injects it):

```go
return &pluginsdk.AccessResponse{
    Env: map[string]string{
        "KUBECONFIG": pluginsdk.AccessDirToken + "/kubeconfig",
    },
    Files: []pluginsdk.File{
        pluginsdk.FileFromString("kubeconfig", patchedKubeconfig, 0o600),
    },
    Endpoints: []pluginsdk.Endpoint{
        {Name: "kubernetes-api", Scheme: "https",
         HostPortLabel: "kubernetes-api", Insecure: true},
    },
}, nil
```

After substitution the user gets a working kubeconfig pointing at their
laptop-reachable address, plus DOCKER_HOST and DOCKERSNAP_INSTANCE in
their shell environment.

## Progress events (deploy / teardown)

Deploy and teardown are streaming endpoints. Use the `*pluginsdk.Progress`
the SDK passes you:

```go
func deployHandler(ctx context.Context, in *pluginsdk.Context, p *pluginsdk.Progress) error {
    p.Step("pulling_image", "Pulling " + image)
    // ... do work ...
    p.Done("pulling_image")

    p.Step("running_container", "Starting on port " + portStr)
    // ... do work ...
    if err := runCmd.Run(); err != nil {
        return p.Fail("running_container", err)
    }
    p.Done("running_container")

    p.Complete("foo container running on port %d", port) // optional; SDK auto-emits if you skip
    return nil
}
```

Each event becomes one line in the daemon's NDJSON stream → CLI's
`→`/`✓`/`✗` progress log + dashboard's modal progress panel. Events
are best-effort dropped under back-pressure; don't depend on them
for correctness, only for UX feedback.

## Testing your plugin

### Unit tests with the in-process SDK harness

`pkg/pluginsdk/sdktest` builds a `*pluginsdk.Context` without spinning
up a real plugin binary. Drive your handlers in-process, assert their
return values:

```go
func TestAccessHandler(t *testing.T) {
    ctx := sdktest.NewContext(t).
        WithInstanceName("foo").
        WithForwardedPort("api", 32768, 6443).
        WithConfig(map[string]interface{}{"port": 8080},
            []pluginsdk.ConfigOption{{Name: "port", Type: pluginsdk.ConfigTypeInt}}).
        Build()

    resp, err := accessHandler(context.Background(), ctx)
    require.NoError(t, err)
    assert.Equal(t, "http://${HOST}:${PORT:api}", resp.Endpoints[0].URL)
}
```

### Integration tests — under `tests/plugins/<name>/`

Each plugin gets a directory with a `main_test.go` (Bootstrap helper
+ `instName` + `cleanup`) and one or more `*_test.go` files. They run
on the VM with `sudo`, against a real daemon, and exercise the full
pipeline:

```go
func TestFoo_DeployAndAccess(t *testing.T) {
    name := instName("smoke")
    cleanup(t, name)
    defer cleanup(t, name)

    _, err := c.Create(ctx, name, &client.WorkloadInline{Plugin: "foo"}, nil)
    require.NoError(t, err)

    access, err := c.Access(ctx, name)
    require.NoError(t, err)
    // ... assert URL / files / env are what you expect ...
}
```

`task e2e:plugins:foo` builds and runs them. The Taskfile auto-discovers
plugin test directories, so just creating `tests/plugins/foo/` is enough.

### Defer order matters when tests create clones

ZFS clones depend on the source's snapshot. `defer cleanup(...)` runs
LIFO, so defer the CLONE last (so it runs FIRST and the source's
snapshot has no dependents at destroy time):

```go
cleanup(t, srcName)
cleanup(t, cloneName)
defer cleanup(t, srcName)   // runs SECOND — source destroyed last
defer cleanup(t, cloneName) // runs FIRST — clone destroyed first
```

Reversed order will cause the source to leak silently and trip the next
test run.

## Build / deploy / iterate

```bash
task plugins:build       # build all plugins to bin/plugins/
task plugins:list        # show what got discovered
task deploy:plugins      # rsync to /usr/local/lib/dockersnap/plugins/ on the VM
task plugins:foo         # build + deploy one plugin
```

After a plugin redeploy, the daemon needs to re-discover:

```bash
dockersnap plugin reload   # OR `task deploy:plugins` already restarts the service
```

If you renamed config options or changed the schema, also reload —
existing instances bound to the old schema will continue working
(stored configs are validated against the cached schema), but new
`create` calls need the fresh schema.

## Where to read more

- `docs/PLUGIN-DESIGN.md` — the canonical spec, including JSON shapes for every command and the exact lifecycle integration points.
- `plugins/echo/` — the minimum viable plugin (~150 lines total).
- `plugins/kind/` — a full-featured plugin including netns-aware probes, kubeconfig templating, and per-container restart-policy management.
- `pkg/pluginsdk/doc.go` — package overview with a one-screen "hello world" plugin.
- `pkg/pluginsdk/sdktest/` — the test harness referenced above.
