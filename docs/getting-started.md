# Getting Started

This walks you through installing dockersnap, creating your first instance,
snapshotting it, reverting, and cloning. Plan on five minutes once the
prerequisites are in place.

## Prerequisites

- A Linux host (the daemon is Linux-only — it relies on netns, systemd,
  iptables, and ZFS).
- The ZFS kernel module + userspace tools (`zfs`, `zpool`).
- A disk, partition, or file-backed vdev to host the ZFS pool.
- Docker CE installed but **not** running as a system-wide daemon
  (dockersnap manages per-instance daemons itself).
- Go 1.22+ to build from source.

The CLI side runs anywhere — Linux, macOS, Windows — and talks to the daemon
over HTTP.

## Install

### Option A: Ansible (recommended for a fresh VM)

```bash
git clone https://github.com/johnbuluba/dockersnap
cd dockersnap/deploy
cp inventory.yml.template inventory.yml
$EDITOR inventory.yml          # set your target host
ansible-playbook playbook.yml
```

The role installs the binary, creates the ZFS pool, configures the systemd
unit, and tunes kernel sysctls (inotify limits) for parallel kind clusters.

### Option B: build and install manually

```bash
# Build
git clone https://github.com/johnbuluba/dockersnap
cd dockersnap
task build                     # produces ./bin/dockersnap

# Install
sudo install -m 0755 bin/dockersnap /usr/local/bin/dockersnap

# Create a minimal config
sudo install -d -m 0755 /etc/dockersnap
sudo tee /etc/dockersnap/config.yaml > /dev/null <<'YAML'
zfs:
  pool: dockersnap
network:
  range: 10.0.0.0/8
  subnet_size: 16
api:
  listen: 0.0.0.0:9847
YAML

# Create the ZFS pool (example — adapt vdev to your hardware)
sudo zpool create dockersnap /dev/sdb
sudo zfs create dockersnap/instances

# Run the daemon (foreground; for production, write a systemd unit —
# see deploy/roles/dockersnap/templates/dockersnap.service.j2)
sudo dockersnap serve
```

## Drive it from your laptop

Point the CLI at the daemon:

```bash
export DOCKERSNAP_REMOTE=http://my-vm:9847
dockersnap health             # confirms the daemon is reachable
```

If `api.token` is set in the daemon config, also export
`DOCKERSNAP_TOKEN=<your-token>`.

## Walkthrough

### 1. Create an instance

```bash
dockersnap create demo --plugin echo
```

`echo` is the minimal reference plugin — it deploys one HTTP container so
you can see the lifecycle work end to end without waiting on a kind cluster.
For a real Kubernetes environment, swap `--plugin echo` for `--plugin kind`.

### 2. Talk to it

```bash
eval $(dockersnap use demo)   # exports DOCKER_HOST and any plugin files
docker ps                     # talks to the *instance's* dockerd, not the host's
```

`use` writes any plugin-provided files (kubeconfig, env files, …) into
`~/.dockersnap/<instance>/` and prints `export` lines for the shell.

### 3. Snapshot the golden state

```bash
dockersnap snapshot demo golden
```

Atomic, recursive, milliseconds. The instance keeps running.

### 4. Break it, then revert

```bash
docker exec <container> rm -rf /important   # simulate damage
dockersnap revert demo golden               # rolls the dataset back
```

`revert` stops the dockerd, runs `zfs rollback -r`, drops the page cache,
and starts the dockerd again. The instance is byte-for-byte back to the
snapshot in a few seconds.

### 5. Clone for parallel work

```bash
dockersnap clone demo golden bench
eval $(dockersnap use bench)
```

`bench` is a writable branch sharing all blocks with the source until you
write to either side. Use it to run an experiment without touching `demo`.

### 6. Tear it all down

```bash
dockersnap delete bench
dockersnap delete demo
```

## Next steps

- **[CLI Reference](cli.md)** — every command with examples.
- **[Troubleshooting](troubleshooting.md)** — common issues.
- **[Authoring a Plugin](plugins/authoring.md)** — wrap your own workload.
