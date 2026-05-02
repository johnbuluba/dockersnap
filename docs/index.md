# dockersnap

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

## Why

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

## Where to next

- **[Getting Started](getting-started.md)** — install, create your first
  instance, snapshot/revert/clone in under five minutes.
- **[CLI Reference](cli.md)** — every verb with examples.
- **[Authoring a Plugin](plugins/authoring.md)** — wrap your own workload
  using the Go SDK.
- **[Architecture](DESIGN.md)** — how the pieces fit together.

## License

[Apache License 2.0](https://github.com/johnbuluba/dockersnap/blob/main/LICENSE).
