# Authoring a Plugin

A workload plugin layers environment-specific behaviour (deploy, teardown,
access, health) on top of a bare dockersnap instance. This page is the
**tutorial** — it builds the `echo` plugin from scratch using the Go SDK at
[`pkg/pluginsdk`](https://pkg.go.dev/github.com/johnbuluba/dockersnap/pkg/pluginsdk).

For the **reference** (full eight-command contract, JSON shapes, NDJSON
streaming format, fields available to every command, error codes), see
[Plugin contract reference](../PLUGIN-DESIGN.md).

## Mental model

A plugin is a **standalone executable** dropped into
`/usr/local/lib/dockersnap/plugins/<name>/<name>`. The daemon runs it once
per lifecycle event, passing JSON on stdin and reading JSON (or NDJSON for
progress streams) from stdout. There is no shared in-process state — each
invocation is fresh. The daemon hands the plugin everything it needs:
the instance name, the Docker socket path, the netns name, the resolved
config, etc.

## Prerequisites

- Go 1.22+
- `dockersnap` already running locally or on a reachable VM
- The repo cloned (we'll use the SDK from there)

## 1. Scaffold

A plugin's `main.go` is short — register the metadata, attach handlers,
call `Run()`:

```go
package main

import (
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
	"github.com/johnbuluba/dockersnap/pkg/version"
)

func main() {
	p := pluginsdk.New(pluginsdk.Plugin{
		Name:    "echo",
		Version: version.String(),
		ConfigOptions: []pluginsdk.ConfigOption{
			{
				Name:    "text",
				Type:    pluginsdk.ConfigTypeString,
				Default: "hello from {{ instance_name }}",
			},
		},
	})
	p.OnDeploy(deploy)
	p.OnAccess(access)
	p.OnHealth(health)
	p.Run()
}
```

`Name` becomes the on-disk binary name. `ConfigOptions` declares what
`--config k=v` keys are valid; the SDK validates inputs and applies
defaults before your handler runs.

## 2. Deploy

`deploy` is invoked when an instance is created with
`--plugin echo`. It receives the instance context and resolved config; it
should leave the instance in a steady state and may stream progress.

```go
func deploy(ctx pluginsdk.Context, p pluginsdk.Progress) error {
	p.Step("starting", "running echo container")
	text := ctx.Config.String("text")

	// ctx.DockerClient() returns a Docker SDK client wired to this
	// instance's dockerd socket — never the host's.
	cli, err := ctx.DockerClient()
	if err != nil {
		return err
	}

	if err := runEchoContainer(ctx, cli, text); err != nil {
		return err
	}

	p.Step("complete", "echo container running")
	return nil
}
```

Anything you write to `Progress` shows up live in the CLI / dashboard.
Each `p.Step` produces one NDJSON event on stdout — see [the contract
reference](../PLUGIN-DESIGN.md) for the schema.

## 3. Access

`access` is what `dockersnap use <inst>` and `dockersnap access <inst>`
call. Return any files to materialize and any env vars to export:

```go
func access(ctx pluginsdk.Context) (pluginsdk.AccessResult, error) {
	port, err := lookupPublishedPort(ctx)
	if err != nil {
		return pluginsdk.AccessResult{}, err
	}
	return pluginsdk.AccessResult{
		Env: map[string]string{
			"ECHO_URL": fmt.Sprintf("http://%s:%d", ctx.Instance.Host, port),
		},
	}, nil
}
```

`use` translates `Env` into `export` lines and `Files` into materialized
files under `~/.dockersnap/<instance>/`. The kind plugin returns
`Files: { "kubeconfig": "..." }` so users get a working
`KUBECONFIG=~/.dockersnap/<inst>/kubeconfig` after `eval $(dockersnap use)`.

## 4. Health

`health` is polled on a schedule (default 30s) and cached. Return one of
the typed statuses; an explanatory `Message` shows up in the dashboard:

```go
func health(ctx pluginsdk.Context) (pluginsdk.HealthResult, error) {
	if ok := ping(ctx); ok {
		return pluginsdk.HealthResult{
			Status: pluginsdk.HealthHealthy,
		}, nil
	}
	return pluginsdk.HealthResult{
		Status:  pluginsdk.HealthUnhealthy,
		Message: "echo container not responding on /",
	}, nil
}
```

The poller threshold (`dockersnap_plugins_health_failure_threshold`,
default 3) decides when consecutive `Unhealthy` results flip the
instance's overall state.

## 5. Teardown

`teardown` runs before `dockersnap delete <inst>` removes the dataset.
Use it to release any **external** state (cloud resources, DNS records).
You don't need to delete containers — the dataset is about to vanish.
The echo plugin doesn't need a teardown handler; if you don't register
one, the SDK no-ops.

## 6. Build, install, test

```bash
# From the repo root
task plugins:build              # builds every plugins/<name>/

# Or just one
go build -o bin/plugins/echo ./plugins/echo

# Install it where the daemon looks for plugins
sudo install -d /usr/local/lib/dockersnap/plugins/echo
sudo install -m 0755 bin/plugins/echo /usr/local/lib/dockersnap/plugins/echo/echo

# Make the daemon re-discover it without restarting
dockersnap plugin reload

# Try it
dockersnap create demo --plugin echo --config text="hi"
dockersnap workload describe demo
dockersnap workload health    demo
eval $(dockersnap use demo)
echo $ECHO_URL
```

## Testing your plugin

`pkg/pluginsdk/sdktest` provides fakes for `Context`, `Progress`, and the
Docker client. Plugin tests are plain Go tests — no exec mocking needed.

```go
func TestDeploy(t *testing.T) {
	ctx := sdktest.NewContext().
		WithInstanceName("demo").
		WithConfig(map[string]any{"text": "hello"})
	prog := sdktest.NewProgress()

	require.NoError(t, deploy(ctx, prog))
	assert.Equal(t, "complete", prog.LastStep())
}
```

The reference plugins under `plugins/echo/` and `plugins/kind/` both ship
unit tests using this pattern — read them as worked examples.

## What next

- **[Plugin contract reference](../PLUGIN-DESIGN.md)** — the full JSON
  shapes, NDJSON streaming format, and every field on every command.
- **`pkg/pluginsdk` godoc** — auto-generated API docs.
- **`plugins/echo/`** — minimal real plugin, ~150 lines total.
- **`plugins/kind/`** — full kind cluster: deploy, kubeconfig, port
  exposure, per-pod health.
