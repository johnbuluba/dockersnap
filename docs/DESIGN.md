# Design Document — dockersnap

## 1. Overview

dockersnap is a CLI/daemon that provides instant snapshot, revert, and clone operations for Docker-based dev environments (e.g. kind clusters) by leveraging ZFS copy-on-write semantics and per-instance Docker daemon isolation.

## 2. Problem Statement

Some Docker-based environments take a long time to deploy — kind clusters with hundreds of pods, layered services, paid-for setup time can easily run into the multi-hour range. When a development or testing session corrupts the state, the only recovery option is a full redeployment. This tool eliminates that bottleneck by:

1. Capturing the fully-deployed state as a ZFS snapshot (seconds)
2. Reverting to that state atomically (seconds + pod reconciliation time)
3. Cloning the state for parallel independent work (seconds, zero extra disk)

## 3. Architecture

### 3.1 Instance Model

An **instance** is the fundamental unit of isolation:

```
Instance "demo"
├── ZFS dataset:     dockersnap/instances/demo (mounted at /dockersnap/instances/demo)
├── Docker daemon:   dockerd --data-root /dockersnap/instances/demo --host unix:///run/dockersnap/demo.sock
├── Docker network:  "demo" (subnet 10.10.0.0/16, auto-allocated)
├── Kind cluster:    "demo" (containers: demo-control-plane, demo-worker, ...)
└── Kubeconfig:      /var/lib/dockersnap/kubeconfigs/demo.yaml
```

### 3.2 Component Diagram

```
┌──────────────────────────────────────────────────────────────────┐
│ dockersnap daemon (PID 1 of systemd service)                      │
│                                                                    │
│  ┌───────────┐  ┌──────────────┐  ┌──────────┐  ┌─────────────┐ │
│  │ REST API  │  │ Instance Mgr │  │ ZFS Ops  │  │ State Store │ │
│  │ (chi)     │─▶│              │─▶│ (exec)   │  │ (JSON file) │ │
│  └───────────┘  │              │─▶│          │  └─────────────┘ │
│                  │              │  └──────────┘                   │
│  ┌───────────┐  │              │  ┌──────────┐                   │
│  │ CLI       │─▶│              │─▶│ Dockerd  │                   │
│  │ (cobra)   │  │              │  │ Manager  │                   │
│  └───────────┘  │              │  └──────────┘                   │
│                  │              │  ┌──────────┐                   │
│                  │              │─▶│ Network  │                   │
│                  └──────────────┘  │ Allocator│                   │
│                                    └──────────┘                   │
└──────────────────────────────────────────────────────────────────┘
```

### 3.3 ZFS Layout

```
dockersnap                          (pool)
└── instances/                      (parent dataset, not mounted)
    ├── demo/                        (instance dataset, mountpoint=/dockersnap/instances/demo)
    │   └── (Docker's /var/lib/docker tree: images, containers, volumes, network, etc.)
    ├── demo@golden                  (snapshot: zero space until demo diverges)
    ├── demo@experiment              (another snapshot)
    ├── demo-dev/                    (clone from demo@golden: CoW, shared blocks)
    └── demo-test/                   (clone from demo@golden: CoW, shared blocks)
```

### 3.4 Docker Daemon Isolation

Each instance runs its own dockerd process inside a **dedicated network namespace**:

```
dockerd \
  --data-root /dockersnap/instances/demo \
  --host unix:///run/dockersnap/demo.sock \
  --pidfile /run/dockersnap/demo.pid \
  --exec-root /run/dockersnap/exec/demo \
  --config-file /var/lib/dockersnap/daemon-configs/demo.json \
  --iptables=true \
  --ip-masq=true
```

The daemon runs inside a systemd transient unit with `NetworkNamespacePath=/run/netns/ds-<name>`.
This provides complete iptables, bridge, and routing isolation between instances:

```
┌─── Host Network Namespace ───────────────────────────────────────┐
│                                                                    │
│  veth-demo (10.28.0.1/30) ←──────→ ┌── netns ds-demo ──────────┐  │
│  veth-dev (10.29.0.1/30) ←──────→ │  veth0 (10.28.0.2/30)    │  │
│                                     │  docker0 (172.17.0.1/16) │  │
│  iptables:                          │  br-kind (172.18.0.1/16) │  │
│    MASQUERADE -s 10.28.0.0/30       │  DOCKER chains (isolated)│  │
│    MASQUERADE -s 10.29.0.0/30       └─────────────────────────-┘  │
│                                     ┌── netns ds-dev ──────────┐  │
│                                     │  veth0 (10.29.0.2/30)    │  │
│                                     │  docker0 (172.17.0.1/16) │  │
│                                     │  DOCKER chains (isolated)│  │
│                                     └──────────────────────────┘  │
└──────────────────────────────────────────────────────────────────-┘
```

The daemon config (`demo.json`) includes:
- `"storage-driver": "overlay2"` (Docker uses overlay2 on top of the ZFS-mounted directory)
- `"bridge": ""` (default docker0 — safe because each dockerd is in its own netns)
- `"cgroup-parent": "/dockersnap-demo"` (unique per instance, prevents cgroup collision between clones)
- `"exec-opts": ["native.cgroupdriver=cgroupfs"]` (avoids systemd scope name collisions)
- `"live-restore": true` (containers survive daemon restart)
- `"dns": ["<host-dnsmasq-ip>"]`
- `"log-driver": "json-file"` with size limits
- `"iptables": true, "ip-masq": true` (safe — isolated per netns)

**Note:** We do NOT use Docker's ZFS storage driver. Instead, we let Docker use overlay2 (its default) with `--data-root` pointed at a ZFS-mounted directory. ZFS operates at the block level beneath Docker, transparent to it. This is simpler, more compatible, and equally effective for snapshot/rollback.

## 4. Operations

### 4.1 Create Instance

```
1. Allocate subnet from pool (next available /16 from configured range)
2. Create ZFS dataset: zfs create dockersnap/instances/<name>
3. Generate Docker daemon config (daemon.json with allocated subnet)
4. Start dockerd process
5. Wait for Docker to be healthy (ping socket)
6. Create Docker network with allocated subnet
7. Record instance in state file
```

### 4.2 Snapshot

```
1. Validate instance exists and dockerd is running
2. Stop dockerd (sends SIGTERM, waits for clean shutdown)
   → All containers stop gracefully (etcd flushes WAL)
3. zfs snapshot -r dockersnap/instances/<name>@<label>
   → Atomic, O(1), zero additional space
4. Start dockerd
   → Docker discovers existing containers
   → Containers with restart policy auto-start
   → kubelet reconciles pods from etcd
5. Record snapshot in state file
```

### 4.3 Revert

```
1. Validate instance and snapshot exist
2. Stop dockerd (all containers stop)
3. zfs rollback -r dockersnap/instances/<name>@<label>
   → All blocks written after snapshot are discarded
   → Dataset is byte-for-byte identical to snapshot time
   → If intermediate snapshots exist, destroy them first (with --force flag)
4. Start dockerd
   → Docker reads its state from disk (rolled back state)
   → Containers auto-start in their pre-snapshot state
   → kubelet reconciles: all pods come back as they were
5. Update state file (remove references to destroyed intermediate snapshots)
```

### 4.4 Clone

```
1. Validate source instance and snapshot exist
2. Allocate new subnet for clone
3. zfs clone dockersnap/instances/<source>@<label> dockersnap/instances/<new-name>
   → Instant: creates a writable branch sharing all blocks via CoW
   → Zero additional disk space until new writes occur
4. Randomize host port bindings in container hostconfig.json (prevent port conflicts)
5. Generate new daemon config for clone (with new subnet and unique cgroup-parent)
6. Set up network namespace with veth pair to host
7. Start new dockerd in namespace on cloned dataset
   → Docker reads existing network state and container configs
   → Containers auto-start (restart policy kicks in)
8. Record clone in state file
```

**Parallel execution:** Thanks to network namespace isolation, source and clone instances
run simultaneously without conflicts. Each dockerd has its own iptables chains, bridges,
and routing tables. The only shared resource is cgroups, which are isolated via unique
`cgroup-parent` paths and the cgroupfs driver.

Each instance gets a unique `cgroup-parent` in its daemon.json (`/dockersnap-<name>`)
to ensure container cgroups are properly namespaced per instance.

### 4.5 Delete

```
1. Stop dockerd if running
2. zfs destroy -r dockersnap/instances/<name>
   → Destroys dataset and all its snapshots
   → If it's a clone, space returns to parent snapshot
3. Clean up: remove socket, pidfile, daemon config
4. Release subnet allocation
5. Remove from state file
```

## 5. Network Auto-Allocation

Subnets are allocated from a configurable range (default: `10.0.0.0/8`):

- Each instance gets a /16 subnet
- Allocation is sequential: first instance = `10.10.0.0/16`, second = `10.11.0.0/16`, etc.
- The base offset (10.10) is configurable
- Released subnets are reused — `state.NextFreeSubnetIndex` returns the lowest unused index, so deleting `demo-2` and creating a new instance reclaims `10.12.0.0/16` instead of growing the high-water mark
- `Allocator.SubnetForIndexChecked` returns an explicit error when an index would overflow the /8 range (e.g. base `250.250.0.0/16` + index 2000), preventing silent byte-truncation wrap-around
- MetalLB IP for each instance: `<subnet>.10.10/32` (e.g., `10.10.10.10`, `10.11.10.10`)

### 5.1 Instance name validation

Instance names are constrained by `instance.ValidateName` to `^[a-z][a-z0-9-]{0,31}$`. The regex is not cosmetic — names are embedded into:

- ZFS dataset paths (`dockersnap/instances/<name>`)
- Filesystem paths (`/run/dockersnap/<name>.sock`)
- systemd transient unit names (`dockersnap-<name>.service`)
- Linux network namespace names (`ds-<name>`)
- veth interface names (truncated to a hash for names > 12 chars; see `dockerd.VethHostName`)
- cgroup parent paths (`/dockersnap-<name>`)
- iptables rule comments

Any character outside the allowed set risks shell quoting, path traversal, or systemd unit collisions. The check runs at every entry point: API handlers, CLI commands, and `Manager.Create` / `Manager.Clone`.

## 5.5 TCP Port Proxy

Each instance's containers (especially the kind API server) publish ports on `0.0.0.0:<port>`
inside their network namespace. These ports are unreachable from the host without forwarding.

The dockersnap daemon includes a **built-in TCP proxy** that forwards published ports from
the host's real IP to the namespace's veth IP:

```
Remote developer                    Host (vm.example.com)                  Netns ds-demo
─────────────────                   ─────────────────────                  ──────────────
kubectl → vm.example.com:34567  →   proxy.Manager listener (0.0.0.0:34567) →  10.28.0.2:34567
                                         (pure Go, io.Copy bidirectional)       ↓
                                                                            docker-proxy → container:6443
```

**Key properties:**
- **Daemon-managed** — starts/stops with instance lifecycle
- **Namespace-aware** — uses `nsenter`+`socat` to reach `127.0.0.1` inside netns where docker-proxy binds
- **Auto-discovery** — scans `docker inspect` for published ports
- **API-visible** — `GET /api/v1/instances/{name}/ports` lists active forwardings
- **Refreshable** — `POST /api/v1/instances/{name}/ports/refresh` rescans
- **Configurable bind** — `api.proxy_bind` controls the listen address (default: `0.0.0.0`)

The `dockersnap use` command patches the kubeconfig to use the proxy's host address,
making `kubectl` work transparently for both local and remote users.

## 6. Configuration

`/etc/dockersnap/config.yaml`:

```yaml
zfs:
  pool: dockersnap
  dataset_prefix: instances
  arc_max_bytes: 5368709120  # 5GB

api:
  listen: "127.0.0.1:9847"
  token: "<generated-by-ansible>"
  proxy_bind: "0.0.0.0"        # TCP proxy bind address (remote access)

network:
  range: "10.0.0.0/8"
  subnet_size: 16             # /16 per instance
  base_offset: "10.10.0.0"   # First allocation starts here
  metallb_host_offset: "10.10"  # .10.10 within each subnet

docker:
  dns: ["172.17.0.1"]        # Docker bridge gateway (dnsmasq)
  log_max_size: "50m"
  log_max_file: 3

state_file: "/var/lib/dockersnap/state.json"
run_dir: "/run/dockersnap"
```

## 7. State File

`/var/lib/dockersnap/state.json`:

```json
{
  "instances": {
    "demo": {
      "name": "demo",
      "dataset": "dockersnap/instances/demo",
      "subnet": "10.10.0.0/16",
      "subnet_index": 0,
      "metallb_ip": "10.10.10.10",
      "socket": "/run/dockersnap/demo.sock",
      "pid_file": "/run/dockersnap/demo.pid",
      "created_at": "2026-04-30T10:00:00Z",
      "status": "running",
      "clone_of": "",
      "kind_cluster": "demo",
      "snapshots": [
        {"label": "golden", "created_at": "...", "tags": {"version": "2.5.0"}}
      ]
    },
    "demo-dev": {
      "name": "demo-dev",
      "dataset": "dockersnap/instances/demo-dev",
      "subnet": "10.11.0.0/16",
      "subnet_index": 1,
      "metallb_ip": "10.11.10.10",
      "socket": "/run/dockersnap/demo-dev.sock",
      "pid_file": "/run/dockersnap/demo-dev.pid",
      "created_at": "2026-04-30T14:00:00Z",
      "status": "running",
      "clone_of": "demo@golden",
      "kind_cluster": "demo",
      "snapshots": []
    }
  },
  "next_subnet_index": 2
}
```

### 7.1 Concurrent state mutations

The `state.Store` exposes `Update(fn)` and `View(fn)` rather than raw `Load`/`Save`. The store mutex is held for the entire load-modify-save cycle, which means two API calls (e.g. simultaneous `create A` and `create B`) cannot race the same `next_subnet_index` read. `TestManager_ConcurrentCreate` exercises this with 8 parallel `Create` calls.

Lifecycle operations (`Create`, `Clone`) follow a three-phase pattern:

1. **Reserve** under the lock: pick a subnet index, write a placeholder `Instance` with `Status: "error"`, advance `next_subnet_index` if needed.
2. **Heavy I/O** outside the lock: ZFS create/clone, dockerd start, port forwarding setup. Other operations on different instances can proceed.
3. **Commit** under the lock: flip `Status` to `"running"`. On failure, a rollback closure deletes the placeholder so a partial Create doesn't leak.

`State.Validate` runs on every load and rejects state files with empty `Name`/`Dataset`/`Subnet` fields, surfacing corruption immediately rather than letting bad rows propagate.

### 7.2 Snapshot label structure

`snapshots` is a list of `{label, created_at, tags}`. The `tags` map is freeform (`--tag key=value` from the CLI). Old state files where `snapshots` is `[]string` are auto-converted on load via `Instance.UnmarshalJSON`.

## 8. API Design

REST API (chi) with token authentication via `Authorization: Bearer <token>` header.

### Endpoints

| Method | Path | Body | Description |
|--------|------|------|-------------|
| GET | /api/v1/instances | - | List all instances |
| POST | /api/v1/instances | `{"name": "demo"}` | Create instance |
| GET | /api/v1/instances/{name} | - | Get instance detail |
| DELETE | /api/v1/instances/{name} | - | Delete instance |
| POST | /api/v1/instances/{name}/snapshot | `{"label": "golden"}` | Create snapshot |
| POST | /api/v1/instances/{name}/revert | `{"label": "golden"}` | Revert to snapshot |
| POST | /api/v1/instances/{name}/clone | `{"label": "golden", "new_name": "demo-dev"}` | Clone |
| POST | /api/v1/instances/{name}/start | - | Start dockerd |
| POST | /api/v1/instances/{name}/stop | - | Stop dockerd |

## 9. Design Decisions

### Why NOT Docker's ZFS storage driver?

Docker's ZFS storage driver creates individual ZFS datasets per image layer and container. While this seems ideal, it has problems:
- Cannot snapshot/rollback at the container level without breaking Docker's internal layer chain metadata
- Docker's metadata DB (`image/zfs/`) must be consistent with dataset state
- Cloning requires manipulating Docker's internal databases

Instead, we let Docker use overlay2 (its default, proven, fastest driver) and manage ZFS at a higher level: the entire Docker data root is one ZFS dataset. This means:
- Snapshot captures EVERYTHING: all images, containers, volumes, networks, metadata
- Rollback restores perfect consistency (Docker's metadata matches)
- Simpler, more robust, no version-specific Docker internals to worry about

### Why one dockerd per instance?

1. **Atomic operations:** Stopping one dockerd stops all containers in that instance atomically
2. **ZFS consistency:** The entire data-root is one dataset — snapshot is guaranteed consistent
3. **Isolation:** Instances cannot interfere with each other
4. **Clone simplicity:** Clone the dataset, start a new daemon — done

### Why overlay2 on ZFS (not native ZFS driver)?

Docker's overlay2 driver works on any POSIX filesystem. When the underlying filesystem is ZFS:
- ZFS still provides block-level deduplication across clones (CoW shares blocks)
- Docker gets its fast overlay performance for day-to-day operations
- We get clean dataset-level snapshots for our tool
- No dependency on Docker's experimental ZFS driver quirks

### Why NOT ZFS deduplication (`dedup=on`)?

ZFS has a built-in deduplication feature, but we intentionally **do not enable it**:

| Issue | Impact |
|-------|--------|
| RAM cost | Dedup table (DDT) requires ~320 bytes per unique block. 40GB with 4K blocks = ~3.2 GB RAM just for DDT |
| Consumes ARC | Our 5GB ARC budget would be eaten by DDT, leaving nothing for read caching |
| Write penalty | Every write requires a DDT lookup — significant I/O overhead |
| Unnecessary | Clones already share blocks via CoW — this IS dedup, just implicit and free |

**What we use instead:**
- `compression=lz4` — 30-50% space savings with near-zero CPU cost
- ZFS clones — implicit block-level dedup via CoW (shared blocks between parent/clone)
- ARC cache — 5GB of RAM dedicated to read acceleration, not DDT bookkeeping

### ZFS Pool Expansion (Adding Disks)

ZFS pools can be expanded at any time by adding new vdevs:

```bash
# Add a single disk (stripe — increases capacity and throughput)
zpool add dockersnap /dev/sdc

# Add a mirror pair (increases capacity + redundancy)
zpool add dockersnap mirror /dev/sdc /dev/sdd
```

**Key properties:**
- **Online expansion:** No downtime, no unmounting, no data migration needed
- **Immediate:** New space is available instantly after `zpool add`
- **Transparent:** All existing datasets, snapshots, and clones automatically benefit from the new space
- **Cannot remove vdevs** (ZFS limitation): once added, a vdev cannot be removed from a pool (except special vdevs like cache/log). Plan disk choices carefully.

**Recommendation for dockersnap:**
- Start with a single disk (simplicity)
- If disk pressure grows (many clones diverging), add another disk as a stripe
- For production/important data, use mirror vdevs for redundancy
- The Ansible role's `dockersnap_zfs_vdev` variable supports multiple disks and vdev types

## 10. Security Considerations

- API defaults to `0.0.0.0:9847` (configurable via `api.listen`). Set to `127.0.0.1:9847` for localhost-only.
- Token auth prevents unauthorized API access (set `api.token` in config).
- CLI supports `--remote` flag and `DOCKERSNAP_REMOTE` env var for remote access.
- Each dockerd runs as root (required for containers) but instances are isolated from each other.
- State file permissions: 0600, owned by root.

## 11. Planned Feature: Snapshot Distribution via ZFS Send/Receive

### Motivation

A heavy environment can take hours to deploy from scratch. If multiple developers or VMs need the same golden state, each would have to deploy independently. ZFS send/receive enables a **deploy-once, distribute-everywhere** model.

### Architecture

```
┌─── Build Server (deploys once) ──────┐       ┌─── Dev VM 1 ─────────────────┐
│                                       │       │                               │
│  dockersnap/instances/demo@golden      │──SSH─▶│  dockersnap/instances/demo     │
│  dockersnap/instances/demo@v2.1        │       │  (received, ready to start)   │
│  dockersnap/instances/demo@v2.2        │       └───────────────────────────────┘
│                                       │       ┌─── Dev VM 2 ─────────────────┐
│                                       │──SSH─▶│  dockersnap/instances/demo     │
└───────────────────────────────────────┘       └───────────────────────────────┘
```

### How ZFS Send/Receive Works

- **Full send:** Serializes all blocks in a snapshot as a byte stream. First-time transfer.
- **Incremental send (`-i`):** Only transfers blocks that changed between two snapshots. Fast for version upgrades.
- **Recursive (`-R`):** Includes child datasets and all intermediate snapshots.
- **Resumable (`-s` / `-t`):** If interrupted (network drop), resumes from where it stopped.
- **Checksummed:** ZFS validates data integrity on receive.

### Planned CLI Commands

```bash
# Push a snapshot to a remote server
dockersnap push <instance> <label> --to <user@host>:<pool>/<path>

# Pull a snapshot from a remote server
dockersnap pull <user@host>:<pool>/<path>@<label> --as <instance-name>

# Pull incrementally (only blocks changed since a known snapshot)
dockersnap pull <user@host>:<pool>/<path>@<label> --incremental-from <prev-label>

# Export snapshot to a file (for offline distribution via NFS/S3)
dockersnap export <instance> <label> --output /path/to/snapshot.zfs

# Import snapshot from a file
dockersnap import /path/to/snapshot.zfs --as <instance-name>
```

### Underlying ZFS Commands

```bash
# Full send over SSH
zfs send -R dockersnap/instances/demo@golden | ssh dev-vm zfs receive dockersnap/instances/demo

# Incremental send (only delta between v2.0 and v2.1)
zfs send -i @v2.0 dockersnap/instances/demo@v2.1 | ssh dev-vm zfs receive dockersnap/instances/demo

# Export to file
zfs send -R dockersnap/instances/demo@golden > /shared/snapshots/demo-golden.zfs

# Import from file
zfs receive dockersnap/instances/demo < /shared/snapshots/demo-golden.zfs
```

### Use Cases

1. **Golden image distribution:** One team deploys the environment once, snapshots, and distributes to all dev VMs. No one else waits hours.
2. **Version catalog:** Central server holds `@v2.0`, `@v2.1`, `@v2.2`. Developers pull the version they need.
3. **Incremental upgrades:** New environment version deployed on build server; `push` only the delta to all VMs. Minutes instead of hours.
4. **Offline/airgap distribution:** Export to a file, copy via USB or airgap transfer, import on target.
5. **CI golden state:** CI pipeline deploys nightly, snapshots, and distributes to all test environments.

### Transfer Size Considerations

| Scenario | Transfer Size | Time (1 Gbps) |
|----------|--------------|---------------|
| Full golden snapshot (~40 GB) | ~40 GB | ~5 min |
| Incremental (minor update) | ~2-5 GB | ~30 sec |
| Incremental (major version bump) | ~10-15 GB | ~2 min |

### Post-Receive Steps

After receiving a snapshot on a dev VM:
1. Patch network state (different subnet for this VM's instance)
2. Generate daemon config
3. Start dockerd
4. Containers auto-start from the received state

This is handled by `dockersnap pull --as <name>` which wraps receive + network patching + daemon start.

