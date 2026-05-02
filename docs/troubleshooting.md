# Troubleshooting

A short list of issues you're likely to hit on first contact, with the
diagnostic and the fix. Open an issue if you trip over something not
covered here.

## "cannot mount … no such pool" / "permission denied" on first run

The daemon expects a ZFS pool named per `zfs.pool` in the config (default
`dockersnap`) with a child dataset `instances`.

```bash
sudo zpool list                            # is the pool there?
sudo zfs list dockersnap/instances         # is the parent dataset there?

# If not:
sudo zpool create dockersnap /dev/sdb      # adapt to your hardware
sudo zfs create dockersnap/instances
```

The daemon must run as root (it shells out to `zfs`/`ip`/`iptables`/`systemd-run`).

## `dockersnap` CLI says "connection refused"

The CLI is asking the daemon at `$DOCKERSNAP_REMOTE` (or `--remote`).

```bash
echo $DOCKERSNAP_REMOTE                    # what URL is the CLI hitting?
curl -s http://my-vm:9847/api/v1/health    # is the daemon up?
sudo systemctl status dockersnap           # ...if installed as a service
sudo journalctl -u dockersnap -n 200       # what does the daemon say?
```

If `api.token` is set in the daemon config, also export `DOCKERSNAP_TOKEN`.

## Containers inside an instance can't reach the internet

Each instance's dockerd lives in netns `ds-<name>` with a veth pair to the
host. NAT and DNS need to be in place.

```bash
sudo ip netns exec ds-<name> ip a          # interfaces present? IP assigned?
sudo ip netns exec ds-<name> ping -c1 1.1.1.1
sudo ip netns exec ds-<name> getent hosts example.com
```

Common causes:

- **systemd-networkd hijacking the veth.** The daemon writes
  `/etc/systemd/network/10-dockersnap-veth.network` to mark `ve-*` as
  unmanaged; if that file is missing or the priority is wrong, the host
  side gets a DHCP/link-local address and routing breaks. Re-running the
  daemon (or the Ansible role) reinstates it.
- **dnsmasq not forwarding upstream.** The daemon's dnsmasq config uses
  `resolv-file=/run/systemd/resolve/resolv.conf`. If `systemd-resolved`
  isn't running or that file is empty, external hostnames don't resolve.
- **Corporate proxy.** Set `dockersnap_proxy_http` /
  `dockersnap_proxy_https` in `deploy/host_vars/<host>.yml` so each
  dockerd inherits the right env vars.

## After `revert`, containerd or etcd panic with bbolt errors

This is the page-cache-after-rollback corruption scenario. The fix is
already in `manager.go`: `sync` → `zfs rollback -r` → `drop_caches` →
start dockerd. If you're seeing it, you're either on an older binary or
something is racing the page-cache drop. Check `journalctl -u dockersnap`
around the revert for the `drop_caches` line.

## Parallel kind clusters fail with "too many open files" / inotify errors

Each kind node runs systemd, which needs an inotify instance. Default
limits (`fs.inotify.max_user_instances=128`) are too low for two parallel
kind instances. The daemon raises these on startup; the Ansible role also
persists them via sysctl. Verify:

```bash
sudo sysctl fs.inotify.max_user_instances    # should be 1024
sudo sysctl fs.inotify.max_user_watches      # should be 524288
```

## A port published inside an instance isn't reachable from the host

Port forwarding is owned by an in-daemon TCP proxy that runs `nsenter` +
`socat` to bridge into the netns. Discovery is event-driven (`docker
events`) with a 30-second polling fallback.

```bash
dockersnap status <name>                    # check the Ports section
curl http://<host>:9847/api/v1/instances/<name>/ports
# Force a re-scan if needed:
curl -X POST http://<host>:9847/api/v1/instances/<name>/ports/refresh
```

If a port shows up in `dockersnap status` but isn't reachable, check host
firewall / cloud security group rules.

## A clone reuses the source's host port and fails to start

Each clone gets a fresh subnet, but Docker's ephemeral host-port allocator
isn't aware of other instances and can hand out the same number twice. The
proxy compensates by detecting `EADDRINUSE` and falling back to `:0`. If
you hit this and the proxy doesn't recover, file an issue with
`dockersnap status <clone> --json`.

## I want to know what the daemon is actually doing

```bash
sudo journalctl -u dockersnap -f            # live tail
# Or set log_level: debug in /etc/dockersnap/config.yaml,
# or DOCKERSNAP_LOG_LEVEL=debug in the unit's environment.
```
