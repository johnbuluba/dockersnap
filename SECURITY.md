# Security Policy

## Reporting a vulnerability

**Please don't open a public GitHub issue for security reports.**

Email **buluba89@gmail.com** with:

- A short description of the issue.
- Steps to reproduce, or a proof-of-concept if you have one.
- The affected version(s) — `dockersnap version` output if you can run it.
- Your assessment of impact (data exposure, privilege escalation, host
  compromise, denial of service, etc.).
- Optionally, a suggested fix.

You should hear back within **72 hours** with either a confirmation, a
question, or an "out of scope" explanation. If a fix is needed, we'll
coordinate disclosure timing with you and credit the report in the release
notes (unless you'd prefer to stay anonymous).

## Scope

In scope:

- The `dockersnap` daemon and CLI binary.
- The Go SDK at `pkg/pluginsdk` (anything plugin authors import).
- The reference plugins under `plugins/` (`echo`, `kind`).
- The Ansible role under `deploy/`.
- The embedded dashboard (`dashboard/`).

Out of scope:

- Third-party plugins not in this repository.
- Issues that require already-root access to the host (the daemon and
  every per-instance dockerd run as root by design).
- Vulnerabilities in upstream Docker, ZFS, kind, or the Linux kernel.
  Please report those upstream; we'll happily update our minimum
  versions once a fix lands.

## Supported versions

Until a `v1.0.0` is tagged, only the `main` branch is supported. Once
versioned releases exist, we'll update this section to list which branches
receive security patches.
