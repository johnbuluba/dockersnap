# Snapshot Internals — How dockersnap Uses ZFS

## 1. ZFS Fundamentals (for context)

ZFS is a copy-on-write (CoW) filesystem. Key properties relevant to dockersnap:

- **Never overwrites in place:** Writes go to new blocks; old blocks remain until explicitly freed
- **Snapshots are O(1):** A snapshot just pins the current block pointer tree. No data copy.
- **Snapshots are free until divergence:** Space is consumed only when the active dataset writes new blocks (old blocks are retained by the snapshot)
- **Rollback is O(1):** Discards the current block pointer tree and restores the snapshot's tree
- **Clones are instant writable forks:** A clone references the snapshot's blocks via CoW; new writes go to new blocks
- **ARC (Adaptive Replacement Cache):** ZFS's in-memory read cache. We set this to 5GB for frequently-accessed container image layers

## 2. What Lives on a Dataset

Each instance's ZFS dataset contains a full Docker data-root:

```
/dockersnap/instances/demo/            ← ZFS mountpoint
├── overlay2/                         ← Docker's overlay2 storage
│   ├── <layer-id>/                   ← Image layers (read-only in Docker's view)
│   │   ├── diff/                     ← Layer contents
│   │   ├── link
│   │   └── lower
│   ├── <container-mount-id>/         ← Container writable layers
│   │   ├── diff/                     ← Container writes (etcd data, logs, etc.)
│   │   ├── merged/                   ← Union mount (readonly layers + writable)
│   │   ├── work/
│   │   └── lower
│   └── l/                            ← Shortened symlinks
├── containers/                       ← Docker container metadata
│   └── <container-id>/
│       ├── config.v2.json            ← Container config (name, env, mounts, network)
│       ├── hostconfig.json           ← Host bindings (ports, devices)
│       ├── hostname
│       ├── resolv.conf
│       └── <container-id>-json.log   ← Container logs
├── image/
│   └── overlay2/                     ← Image metadata DB
│       ├── imagedb/
│       ├── layerdb/
│       └── repositories.json         ← Tag -> image mapping
├── network/
│   └── files/
│       └── local-kv.db              ← BoltDB: network allocations, endpoints
├── volumes/                          ← Named volumes
├── buildkit/                         ← BuildKit cache
├── tmp/
└── runtimes/
```

**Everything** inside a kind node container (etcd, kubelet state, pulled images, pod filesystems) exists within the overlay2 layer for that container. This means a ZFS snapshot of the dataset captures the complete cluster state.

## 3. Snapshot Mechanics

### What happens at the ZFS level

```bash
zfs snapshot -r dockersnap/instances/demo@golden
```

This creates a recursive snapshot (the `-r` flag captures child datasets if any exist, though with our overlay2 approach there typically aren't children — everything is in one dataset).

At the block level:
1. ZFS records the current "transaction group" (TXG) number
2. All block pointers at this TXG are pinned (reference count incremented)
3. No data is copied — this completes in milliseconds regardless of dataset size
4. From this point, writes to the dataset allocate NEW blocks (CoW)
5. The snapshot retains references to the OLD blocks

### Space accounting

```
Dataset "demo":          40 GB USED (at snapshot time)
Snapshot "demo@golden":   0 GB REFER (initially — it shares all blocks with the dataset)

After some writes to "demo":
Dataset "demo":          43 GB USED (3 GB of new blocks written)
Snapshot "demo@golden":   3 GB USED (the 3 GB of old blocks that "demo" overwrote are now held only by the snapshot)
```

Check with: `zfs list -t all -o name,used,refer dockersnap/instances`

## 4. Rollback Mechanics

### What happens at the ZFS level

```bash
zfs rollback -r dockersnap/instances/demo@golden
```

1. All blocks written AFTER the snapshot are freed (reference count decremented, space reclaimed)
2. The dataset's block pointer tree is reset to the snapshot's tree
3. The dataset is now byte-for-byte identical to the moment of snapshot
4. The `-r` flag destroys any intermediate snapshots taken after `@golden`
5. Completes in milliseconds regardless of how much data changed

### Why we must stop dockerd first

Docker holds file descriptors, memory-mapped files, and in-memory caches:
- overlay2 mounts are active (kernel VFS references)
- containerd has shim processes with open FDs to container runtimes
- Docker's internal BoltDB (network/files/local-kv.db) may have uncommitted transactions
- Container logs are being actively written

If we rollback while dockerd is running:
- Open file descriptors would point to freed/reallocated blocks → **corruption**
- In-memory state diverges from on-disk state → **undefined behavior**
- Overlay mounts become stale → **kernel errors**

After stopping dockerd:
- All FDs are closed
- All mounts are unmounted (Docker cleans up on shutdown)
- All in-memory state is flushed
- Safe to rollback

### After rollback: restart behavior

When dockerd starts on the rolled-back dataset:
1. Docker reads `containers/*/config.v2.json` — finds all containers that existed at snapshot time
2. Containers with `RestartPolicy: on-failure` (kind's default) will be restarted by Docker
3. Each container's overlay layers are remounted
4. Inside kind nodes: kubelet starts, reads etcd (rolled back to snapshot state), reconciles pods
5. All 100+ pods come back to exactly their snapshotted state

### live-restore behavior

Each per-instance dockerd runs with `"live-restore": true`. This means:
- Containers and their network namespaces survive a dockerd restart
- After dockerd comes back, it reconnects to existing container processes
- Networks and overlay mounts are preserved across daemon restarts
- This is essential for the dockersnap service restart (e.g., binary upgrade) to not kill running clusters

**Important:** live-restore does NOT apply during snapshot/revert. We explicitly stop all containers before stopping dockerd for ZFS mutations (see "Robust Stop Procedure" below).

### Network namespace isolation

Each dockerd runs inside a dedicated Linux network namespace (`ds-<name>`). This provides:
- Fully isolated iptables tables (nat, filter) — no DOCKER chain conflicts
- Independent docker0 bridge per instance — no bridge IP collisions
- Per-instance routing tables — no route conflicts
- True parallel execution of clones without mutual exclusion

The namespace is connected to the host via a veth pair:
- Host side: `veth-<name>` with IP `10.X.0.1/30` (from instance's /16 allocation)
- Namespace side: `veth0` with IP `10.X.0.2/30`
- Host MASQUERADE rule forwards namespace traffic to the physical NIC
- Default route in namespace points to the host-side veth IP

### Robust stop procedure

Each instance's dockerd runs inside a **systemd transient unit** (`dockersnap-<name>.service`) via `systemd-run`. This places ALL processes — dockerd, containerd-shim, kind containers, pods inside containers — into a single cgroup.

**Why cgroups solve the orphan problem:**
- Container processes (containerd-shim) can outlive dockerd if killed naively
- Overlay mounts inside container mount namespaces hold the ZFS dataset busy
- Network namespace mounts (`/run/dockersnap/exec/<name>/netns/*`) prevent unmount
- With a cgroup, `systemctl stop` kills EVERYTHING — no orphans possible

The stop procedure:
1. **Stop all containers gracefully:** `docker stop` each container (clean etcd shutdown, graceful pod termination)
2. **`systemctl stop dockersnap-<name>.service`:** Sends SIGTERM to the entire cgroup tree. After TimeoutStopSec (60s), sends SIGKILL to anything remaining.
3. **Clean up leftover mounts:** Network namespace mounts can survive process death (they're kernel objects) — unmount them explicitly.

Key systemd properties:
- `KillMode=control-group` — kills ALL processes in the cgroup, not just the main PID
- `Delegate=yes` — allows dockerd to create sub-cgroups for containers
- `TimeoutStopSec=60` — gives containers time to shut down, then force-kills

Only after this is the ZFS dataset safe to destroy/rollback/snapshot.

## 5. Clone Mechanics

### What happens at the ZFS level

```bash
zfs clone dockersnap/instances/demo@golden dockersnap/instances/demo-dev
```

1. A new dataset `demo-dev` is created
2. Its block pointer tree starts as a copy of `demo@golden`'s tree (just the pointers, not data)
3. Reads from `demo-dev` return the snapshot's data (shared blocks)
4. Writes to `demo-dev` allocate new blocks (CoW divergence)
5. The parent snapshot `demo@golden` cannot be destroyed while clones reference it

### Space efficiency

```
Primary "demo":           40 GB (full deployment)
Snapshot "demo@golden":    0 GB (shared)
Clone "demo-dev":          0 GB (initially, all reads served from shared blocks)
Clone "demo-test":         0 GB (initially)

Total disk: 40 GB for 3 complete clusters

After dev work:
Clone "demo-dev":          2 GB (only new/modified blocks)
Clone "demo-test":         1 GB

Total disk: 43 GB for 3 clusters (not 120 GB)
```

### Network patching after clone

Because each clone's dockerd runs in its own network namespace, the Docker network state
from the snapshot (BoltDB at `network/files/local-kv.db`) works without modification.
Docker recreates bridges (`docker0`, `kind`) inside the namespace with no subnet conflicts.

Before starting the cloned dockerd, we randomize host port bindings in container
`hostconfig.json` files (clearing `HostPort` values to `""`). This makes Docker assign
random available ports, preventing port conflicts if ports are forwarded to the host.

The clone's dockerd starts with its own subnet allocation, unique cgroup-parent, and
isolated network namespace — enabling true parallel execution alongside the source.

## 6. Edge Cases

### Intermediate snapshots and rollback

If multiple snapshots exist:
```
demo@golden → demo@experiment1 → demo@experiment2 → (current state)
```

`zfs rollback -r demo@golden` will:
- Destroy `@experiment2`
- Destroy `@experiment1`
- Reset to `@golden`

The `-r` flag handles this automatically. Our state file is updated to remove the destroyed snapshots.

### Clone dependencies

A snapshot cannot be destroyed while clones reference it:
```
demo@golden ← demo-dev (clone)
           ← demo-test (clone)
```

To destroy `@golden`, you must first destroy or promote all clones:
- `zfs destroy demo-dev` (destroys the clone)
- OR `zfs promote demo-dev` (clone becomes independent, snapshot moves to it)

### Disk pressure

ZFS does not over-commit. If the pool fills up:
- Writes fail with ENOSPC
- Snapshots prevent space reclamation (they hold old blocks)
- Resolution: delete old snapshots or clones to free space

Monitor with: `zpool list dockersnap` and `zfs list -o name,used,avail`

## 7. Performance Characteristics

| Operation | Time | Disk I/O |
|-----------|------|----------|
| Snapshot creation | Milliseconds | None (metadata only) |
| Rollback | Milliseconds | None (metadata only) |
| Clone creation | Milliseconds | None (metadata only) |
| dockerd stop | 5-10 seconds | Flush + unmount |
| dockerd start + pod reconciliation | 30-60 seconds | Reads from ARC cache |
| **Total revert cycle** | **~60 seconds** | Mostly cached reads |

The ARC cache (5GB) ensures that frequently-accessed image layers and container metadata are served from RAM, making post-revert pod startup fast.

## 8. Comparison with Alternatives

| Approach | Revert Time | Parallel Clusters | Disk Efficiency | Complexity |
|----------|-------------|-------------------|-----------------|------------|
| **dockersnap (ZFS)** | ~60s | Yes (clones) | Excellent (CoW) | Medium |
| Redeploy from scratch | ~3 hours | Yes (separate) | Poor (full copy) | Low |
| Docker checkpoint (CRIU) | Minutes | No | Poor | High (unstable) |
| VM snapshots | Minutes | Requires nested virt | OK | Low |
| Btrfs subvolumes | ~60s | Yes (snapshots) | Good (reflinks) | Medium |

ZFS was chosen over Btrfs for:
- More mature snapshot/clone management
- Proven ARC cache with tunable RAM allocation
- Better tooling for space accounting (`zfs list`, `zpool iostat`)
- More predictable performance under heavy container workloads

## 9. Page Cache Invalidation After Rollback

**Critical discovery:** After `zfs rollback`, the Linux page cache may retain stale pages
from the pre-rollback state. When containers (particularly containerd and etcd inside kind
nodes) restart and mmap their database files (bbolt), they can get a mix of cached
pre-rollback pages and on-disk post-rollback pages, causing panics like:

```
panic: assertion failed: Page expected to be: 34, but self identifies as 824654209024
```

**Fix:** After every `zfs rollback`, dockersnap drops the kernel page cache before starting
any containers:

```go
exec.CommandContext(ctx, "sh", "-c", "echo 3 > /proc/sys/vm/drop_caches").Run()
```

This forces the kernel to discard all cached pages. The next file read will fetch the
rolled-back data directly from ZFS.

The full revert sequence is:
1. Stop all containers (docker stop)
2. Kill entire cgroup (systemctl stop)
3. Sync filesystem (flush dirty pages to disk)
4. ZFS rollback
5. Drop page cache (invalidate stale cached pages)
6. Start dockerd (containers restart from clean rolled-back state)

## 10. systemd-networkd Interaction

On Ubuntu 24.04+, `systemd-networkd` is active and auto-manages any new network
interfaces it detects. This includes our `ve-*` veth pairs, causing it to override
manually-assigned IPs with DHCP/link-local addresses.

**Fix:** On daemon startup, write `/etc/systemd/network/10-dockersnap-veth.network`:

```ini
[Match]
Name=ve-*

[Link]
Unmanaged=yes
ActivationPolicy=manual

[Network]
DHCP=no
LinkLocalAddressing=no
LLDP=no
EmitLLDP=no
IPv6AcceptRA=no
IPv6SendRA=no
```

Key points:
- Must use priority `10-` (low number = high priority) to override default configs
- `Unmanaged=yes` alone was insufficient with priority `90-`
- Requires `systemctl restart systemd-networkd` (not just reload)
- Also flush addresses on veth before assigning ours (belt-and-suspenders)
