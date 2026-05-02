# CLI Reference

Run `dockersnap --help` for the live, grouped command list. This page is the
narrative reference: every verb, what it does, and a copy-pasteable example.

## Global flags

| Flag | Env var | Description |
|---|---|---|
| `--remote`, `-r` | `DOCKERSNAP_REMOTE` | Daemon URL, e.g. `http://vm.example.com:9847`. |
| `--token`, `-t` | `DOCKERSNAP_TOKEN` | Bearer token, if `api.token` is set in the daemon config. |
| `--help`, `-h` | — | Help for any command. |

## Lifecycle

### `dockersnap create <name> [--plugin <p>] [--config k=v | --config-file <path>]`

Creates a new instance. `<name>` must match `^[a-z][a-z0-9-]{0,31}$`. With
`--plugin`, the named workload runs its `validate` + `deploy` lifecycle
hooks after the bare instance is up.

```bash
dockersnap create demo                                 # bare instance, no workload
dockersnap create demo --plugin echo                   # echo plugin defaults
dockersnap create kc --plugin kind --config retries=5  # kind, with one option
dockersnap create kc --plugin kind --config-file ./kind.yaml
```

### `dockersnap delete <name>` (alias `rm`, `destroy`)

Stops and removes the instance, all its snapshots, the dockerd unit, and
the netns. Plugin `teardown` runs first.

### `dockersnap start <name>` / `stop <name>` / `restart <name>`

Lifecycle of the instance's dockerd. `restart` ignores "already stopped"
errors on the stop phase.

## State

### `dockersnap snapshot <name> <label> [--tag k=v]`

Captures a recursive ZFS snapshot. Optional `--tag` records arbitrary
metadata (multiple allowed):

```bash
dockersnap snapshot demo golden --tag version=2.5.0 --tag owner=team-a
```

Aliased as `snap`.

### `dockersnap revert <name> <label> [--force]`

Rolls the dataset back to the named snapshot. `--force` first destroys any
intermediate snapshots that would block `zfs rollback -r`. The dockerd is
stopped, the page cache is dropped, and the dockerd is restarted as part
of the operation. Aliased as `rollback`.

### `dockersnap clone <name> <label> <new-name>`

Creates a new instance as a CoW clone of `<name>@<label>`. Gets a fresh
subnet, fresh dockerd unit, and inherits the snapshot's data. Both
instances run independently after the clone.

## Access

### `dockersnap use <name>`

Prints `export` statements for the shell:

```bash
eval $(dockersnap use demo)
```

This sets `DOCKER_HOST` to the instance's socket and exports any plugin-
provided env vars (e.g. `KUBECONFIG` for the kind plugin). Plugin files
are materialized to `~/.dockersnap/<name>/`.

### `dockersnap access <name> [--file <name>] [-o json]`

Inspects what the workload plugin exposes. Without flags, prints a summary;
`--file <name>` prints a single materialized file (e.g. `kubeconfig`); `-o
json` prints the structured plugin response.

```bash
dockersnap access demo                       # summary
dockersnap access demo --file kubeconfig     # cat the kubeconfig
dockersnap access demo -o json               # raw plugin output
```

### `dockersnap docker <name> -- <args>`

Run the host's `docker` CLI against the instance's daemon. Equivalent to
`DOCKER_HOST=<instance-socket> docker <args>`.

```bash
dockersnap docker demo -- ps -a
dockersnap docker demo -- logs <container>
```

### `dockersnap workload describe <name>` / `workload health <name> [--fresh]`

Plugin metadata and cached health respectively. `--fresh` forces a sync
re-check instead of returning the most recent poller result.

## Admin

### `dockersnap list` (alias `ls`, `ps`)

Lists every instance with status, plugin, subnet, and creation time.

### `dockersnap status <name>` (alias `info`, `show`)

Detailed view of one instance: dataset, snapshots, ports, plugin state.

### `dockersnap health` / `dockersnap version`

Daemon-level overview / build version.

### `dockersnap plugin list` / `plugin describe <name>` / `plugin reload`

Plugin admin. `reload` re-discovers binaries in the plugin directory
without restarting the daemon.

### `dockersnap serve`

Runs the daemon. Reads `/etc/dockersnap/config.yaml`. Typically run from
a systemd unit — see `deploy/roles/dockersnap/templates/dockersnap.service.j2`.

## Output formats

`access` already supports `-o json`. Other commands (`list`, `status`,
`workload describe`, `workload health`) currently emit fixed human-readable
output; structured `-o json` / `-o yaml` is on the roadmap.

## Aliases summary

| Alias | Command |
|---|---|
| `ls`, `ps` | `list` |
| `rm`, `destroy` | `delete` |
| `snap` | `snapshot` |
| `rollback` | `revert` |
| `new` | `create` |
| `kc` | `kubeconfig` (kind plugin convenience) |
| `info`, `show` | `status` |
| `wl` | `workload` |
