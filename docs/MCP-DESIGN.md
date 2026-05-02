# dockersnap MCP server — Design

A Model Context Protocol server that exposes the dockersnap API as
tools and resources for LLM clients (Claude Code, Claude Desktop,
Cursor, etc.). Lets a developer drive instances from inside an AI
conversation: "snapshot before I try this fix, revert if it fails,"
"spin up three parallel kind clusters and run the test matrix,"
"show me the kubeconfig for the dev environment."

This is a design document. Implementation is parked — see
`SESSION.md` for the trigger conditions.

## 1. Goals

- Make every dockersnap operation reachable from an MCP-aware LLM
  without the user having to leave their conversation to shell out.
- Keep the surface mappable to existing API endpoints — no new
  business logic, no new state. The MCP server is a translation
  layer.
- Ship as a subcommand of the existing CLI (`dockersnap mcp`), not
  a separate binary. Developers who already have the CLI
  installed gain MCP support for free.
- Use the same auth surface the CLI does (`DOCKERSNAP_REMOTE` /
  `DOCKERSNAP_TOKEN`); no new credential model.

## 2. Non-goals

- **MCP-side state.** The server is stateless; everything lives in
  the daemon. Multiple MCP clients hitting the same daemon see the
  same world.
- **Subscriptions / push updates.** MCP supports notifications, but
  polling tools is fine for our cadence (a developer rarely needs
  sub-second refreshes during a conversation).
- **Plugin authoring as a tool surface.** The plugin author skill at
  `.claude/skills/dockersnap-plugin-author/SKILL.md` already covers
  this and reaches the LLM through Claude Code's skill mechanism.
  Don't duplicate.
- **Cross-MCP orchestration.** No "dockersnap MCP triggers kubectl
  MCP." Each server is independently consumable; orchestration is
  the LLM's job.
- **A separate web UI for the MCP server.** That's what the dashboard
  is for.

## 3. Stack

| Layer | Choice | Why |
|---|---|---|
| Language | Go | Same toolchain as the daemon and CLI; no new build system. |
| MCP SDK | `github.com/modelcontextprotocol/go-sdk` (or `mark3labs/mcp-go` if more mature when we ship) | Pick whichever is closer to the spec at start time; both are thin wrappers around the JSON-RPC plumbing. |
| Transport | stdio (default) | Claude Code launches the server as a subprocess. HTTP+SSE is an option later if a remote-MCP use case appears, but stdio is enough for v1. |
| Daemon RPC | reuse `internal/client` | The MCP server is a `client.Client` consumer, no direct daemon access. |

Subcommand path: `cmd/mcp/mcp.go` registered in `cmd/root.go`; `dockersnap mcp` runs the server. Configuration via existing `--remote` / `--token` flags or env vars.

## 4. Tool surface

Tools are LLM-callable verbs that mutate or query state. Curated from
the API — not 1:1; some endpoints don't make sense as LLM tools.

### 4.1 Read-only (always safe to call)

| Tool | Returns | Notes |
|---|---|---|
| `dockersnap_daemon_health` | `DaemonHealth` JSON | Quick state check; first call in any conversation. |
| `dockersnap_list_instances` | `[]Instance` | Always small enough to inline in the LLM context. |
| `dockersnap_get_instance` | `Instance` | One instance with full state. |
| `dockersnap_get_access` | `AccessResponse` | Env / files / endpoints, with tokens already resolved. |
| `dockersnap_get_kubeconfig` | string | Convenience wrapper that pulls `files["kubeconfig"]` from access — common enough to deserve its own tool. |
| `dockersnap_get_workload_health` | `WorkloadHealthResponse` | Optional `fresh: bool` arg. |
| `dockersnap_list_ports` | `PortsResponse` | Forwarded ports for one instance. |
| `dockersnap_list_plugins` | `[]PluginInfo` | What's installed. |
| `dockersnap_describe_plugin` | `PluginInfo` | Schema + config options for one plugin. |

### 4.2 Lifecycle (mutating, generally safe)

| Tool | Args | Notes |
|---|---|---|
| `dockersnap_create_instance` | `name, plugin?, config?` | Streams progress server-side, returns a single summary `{status, events[], duration}` at the end. |
| `dockersnap_start_instance` | `name` | Same streaming model. |
| `dockersnap_stop_instance` | `name` | |
| `dockersnap_restart_instance` | `name` | Server-side stop+start, single tool call. |
| `dockersnap_snapshot` | `name, label, tags?` | |
| `dockersnap_clone` | `src, label, new_name` | |

### 4.3 Destructive (require explicit confirm)

| Tool | Args | Confirm pattern |
|---|---|---|
| `dockersnap_delete_instance` | `name, confirm_name` | `confirm_name` must equal `name`. Mirrors the dashboard's type-to-confirm UX. |
| `dockersnap_revert` | `name, label, force?` | `force=true` requires a separate `confirm_destroy_newer: true` arg, since it silently destroys newer snapshots. |

The tool descriptions explicitly call out destructive semantics in
plain English so the LLM only invokes them when the user clearly
asked. Pattern: `"DESTRUCTIVE — destroys the instance, all
snapshots, and the workload. Only invoke when the user explicitly
asked to delete; the user must approve via the standard tool-call
confirmation in their MCP client."`

### 4.4 Excluded from the tool surface

- `dockersnap serve` / `dockersnap mcp` — meta-commands; not LLM-callable.
- `dockersnap plugin reload` — admin op; rare, gated.
- `dockersnap ports refresh` — internal mechanism the LLM shouldn't care about.
- Any future write-to-`/etc/dockersnap/config.yaml` operations — out of scope.

## 5. Resource surface

Resources are read-only documents the LLM can browse without an explicit
tool call. Useful for context-loading at conversation start.

| URI | Content |
|---|---|
| `dockersnap://daemon/health` | `DaemonHealth` JSON |
| `dockersnap://instances` | List of all instances |
| `dockersnap://instances/{name}` | Single instance state |
| `dockersnap://instances/{name}/access` | Access bundle |
| `dockersnap://instances/{name}/access/kubeconfig` | Kubeconfig file content (text/plain) |
| `dockersnap://instances/{name}/health` | Workload health |
| `dockersnap://plugins` | Plugin list |
| `dockersnap://plugins/{name}` | Plugin schema |
| `dockersnap://docs/plugin-design` | The full `PLUGIN-DESIGN.md` (so a plugin-author conversation has the spec inline) |
| `dockersnap://docs/dashboard-design` | The dashboard design doc |

The tool/resource overlap (`dockersnap_list_instances` ≈
`dockersnap://instances`) is intentional. Tools fit when the LLM
decides to read; resources fit when the user wants to attach them
to the conversation up-front.

## 6. Streaming model

Daemon endpoints stream NDJSON for long-running operations
(create / delete / snapshot / revert / clone / start / stop). MCP
tools return a single response.

**Decision: synchronous wait.** The MCP tool call blocks until the
NDJSON stream terminates with `complete` or `error`, then returns:

```json
{
  "status": "ok",        // or "error"
  "duration_ms": 4321,
  "events": [
    {"step": "stopping_dockerd", "status": "running", "message": "..."},
    {"step": "stopping_dockerd", "status": "done", "message": "Dockerd stopped"},
    ...
  ],
  "result": { /* the post-op state (instance object for create/clone, etc.) */ }
}
```

Latency = full op duration. For sub-minute ops (most), this is
fine — the LLM client shows a "tool running…" indicator.

For deploys that can take several minutes (kind cluster cold start
behind a slow proxy), the synchronous wait is awkward. A
`dockersnap_get_async_job` polling pattern is the right answer
when this becomes a real complaint, not before. Park as future
work.

## 7. Stages

Each stage is independently shippable.

### Stage 1 — Read-only foundation (~half day)

- `cmd/mcp/` subcommand wired to `cmd/root.go` (no GroupID; admin-shaped).
- MCP server skeleton over stdio: handshake, tool listing, resource listing.
- Implement read tools: `daemon_health`, `list_instances`, `get_instance`, `list_plugins`, `describe_plugin`.
- Implement resources: `dockersnap://daemon/health`, `dockersnap://instances`, `dockersnap://plugins/{name}`.
- README snippet for adding the server to Claude Code's `mcpServers` config.

**Done when:** "What's running on dockersnap?" in a Claude conversation
returns a useful summary.

### Stage 2 — Non-destructive lifecycle (~half day)

- `create_instance`, `start`, `stop`, `restart`, `snapshot`, `clone`.
- `get_access`, `get_kubeconfig`, `get_workload_health`, `list_ports`.
- Sync stream consumption with the response shape from §6.
- Resources: `dockersnap://instances/{name}/access/kubeconfig`,
  `dockersnap://instances/{name}/health`.

**Done when:** "Create a kind instance, snapshot it as golden, clone
two branches off it" works end-to-end from one conversation.

### Stage 3 — Destructive ops with safety (~few hours)

- `delete_instance` (with `confirm_name` arg).
- `revert` (with `force` + `confirm_destroy_newer`).
- Tool descriptions surface destructive semantics prominently.

**Done when:** the LLM only deletes when explicitly asked, and the
user can recover from a wrong delete-attempt without lost work
(the confirm-name guard would have caught it).

### Stage 4 — Authoring context (~few hours)

- Resources for the design docs: `PLUGIN-DESIGN.md`, `DASHBOARD-DESIGN.md`.
- The plugin-author skill at `.claude/skills/.../SKILL.md` already
  loads via Claude Code's skill mechanism — but a non-Claude-Code
  MCP client doesn't have skills, so exposing the same content as
  a resource (`dockersnap://docs/plugin-author-guide`) lets any MCP
  client reach it.

**Done when:** a developer in Cursor (no Claude Code skills) can ask
"how do I write a dockersnap plugin?" and get a useful answer
backed by the reference doc.

### Stage 5 — Polish (~half day)

- Tool-call telemetry — log every MCP tool invocation in the
  daemon's slog with `mcp_session_id` so multi-tool conversations
  are debuggable.
- Optional: `--read-only` flag that disables every mutating tool.
  Useful for exposing dockersnap to LLMs in production-ish
  contexts where you don't want them to touch state.
- Optional: `--auto-approve` deny-list that exempts safe read-only
  tools from per-call confirmation in clients that support it.

## 8. Open questions

- **MCP SDK choice.** `github.com/modelcontextprotocol/go-sdk` is the
  official one but young; `mark3labs/mcp-go` has been around longer.
  Pick at implementation time based on stability and feature parity.
- **Tool naming convention.** `dockersnap_create_instance` is verbose
  but unambiguous when the LLM client lists tools from multiple
  servers. Alternative: drop the prefix, since each server already
  scopes its tool names. The longer form wins on disambiguation; go
  with it.
- **Long-deploy UX.** For multi-minute kind deploys, sync wait is
  awkward — Claude Code shows a spinner for 5 minutes. Two options
  if it becomes a real complaint: (a) a `dockersnap_get_async_job`
  polling pattern, (b) emit periodic progress messages via MCP's
  notification channel during the stream. Punt until someone hits it.
- **Per-tool destructive confirmation in non-Claude clients.** Claude
  Code requires user approval per tool call by default. Cursor and
  others may not. The `confirm_name` arg makes the guard
  client-independent — keep it even if the host normally prompts.
- **Multi-daemon support.** A developer with several `DOCKERSNAP_REMOTE`
  endpoints might want one MCP server entry per daemon. The
  configuration story (one `mcp.json` block per remote vs. one
  multi-target block) is best deferred until someone actually has
  that setup.

## 9. Configuration sketch (for users)

End-user setup once we ship Stage 1. In `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "dockersnap": {
      "command": "dockersnap",
      "args": ["mcp"],
      "env": {
        "DOCKERSNAP_REMOTE": "http://my-vm:9847",
        "DOCKERSNAP_TOKEN": "<optional>"
      }
    }
  }
}
```

That's it. No daemon config changes; no new files. The MCP server
is a thin client that authenticates the same way the CLI does.

## 10. When to actually build this

Triggers, in rough priority order:

1. The user (or a teammate) finds themselves repeatedly typing
   `dockersnap` commands during AI-pair-programming sessions
   where the LLM could have driven them.
2. A second developer joins the project and the "spin up an
   environment" workflow benefits from being a one-sentence ask.
3. We ship a `dockersnap-plugin-author` workflow that involves
   the LLM testing plugin code against real instances —
   spinning up scratch instances per iteration is a perfect
   MCP fit.

Until then, the CLI + dashboard cover the workflows. Don't build
this speculatively.
