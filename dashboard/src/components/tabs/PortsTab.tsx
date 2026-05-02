import { useInstancePorts, useRefreshPorts } from "../../lib/queries";
import { Code, ErrorPanel, Panel, Skeleton } from "../ui";

// Forwarded ports table — the proxy state, not what plugins advertise.
// The Refresh button hits /ports/refresh which re-scans containers.
export function PortsTab({ instanceName }: { instanceName: string }) {
  const ports = useInstancePorts(instanceName);
  const refresh = useRefreshPorts(instanceName);

  return (
    <Panel>
      <div class="px-3 py-2 border-b border-border flex items-center justify-between">
        <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
          forwarded ports
        </span>
        <button
          type="button"
          onClick={() => refresh.mutate()}
          disabled={refresh.isPending}
          class="text-xs font-mono px-2 py-0.5 rounded-sm border border-border hover:border-border-strong text-text-secondary hover:text-text disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {refresh.isPending ? "refreshing…" : "refresh"}
        </button>
      </div>

      {ports.isLoading ? (
        <div class="px-4 py-6"><Skeleton class="w-full h-16" /></div>
      ) : ports.error ? (
        <div class="px-3 py-3"><ErrorPanel error={ports.error} /></div>
      ) : !ports.data?.ports || ports.data.ports.length === 0 ? (
        <div class="px-4 py-8 text-center space-y-1">
          <p class="text-sm text-text-secondary">No ports forwarded yet.</p>
          <p class="text-xs text-text-muted">
            Click <span class="font-mono">refresh</span> if a container just started.
          </p>
        </div>
      ) : (
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs uppercase tracking-wide text-text-muted font-mono border-b border-border">
              <th class="px-3 py-2 font-normal">host</th>
              <th class="px-3 py-2 font-normal">target</th>
              <th class="px-3 py-2 font-normal">in container</th>
              <th class="px-3 py-2 font-normal">proto</th>
              <th class="px-3 py-2 font-normal">description</th>
            </tr>
          </thead>
          <tbody class="[&_tr:nth-child(even)]:bg-surface-subtle">
            {ports.data.ports.map((p) => (
              <tr key={`${p.host_port}-${p.protocol}`}>
                <td class="px-3 py-1.5 font-mono tabular-nums">
                  <Code>:{p.host_port}</Code>
                  {p.requested_host_port && p.requested_host_port !== p.host_port && (
                    // The caller (dockerd inside the netns) asked for a
                    // different host port; we couldn't bind it and picked
                    // a free one. Surface the original ask so debugging
                    // "why is my port not 8080?" doesn't require log diving.
                    <span
                      class="ml-2 text-xs font-mono text-status-error"
                      title={`Requested ${p.requested_host_port} but the host port was already in use. Falling back to ${p.host_port}.`}
                    >
                      ← was :{p.requested_host_port}
                    </span>
                  )}
                </td>
                <td class="px-3 py-1.5 font-mono text-text-secondary tabular-nums">
                  {ports.data?.target_ip}:{p.container_port}
                </td>
                <td class="px-3 py-1.5 font-mono text-text-muted tabular-nums">
                  {p.in_container_port ?? "—"}
                </td>
                <td class="px-3 py-1.5 font-mono text-text-muted">{p.protocol}</td>
                <td class="px-3 py-1.5 text-text-secondary text-sm">
                  {p.description ?? <span class="text-text-muted">—</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  );
}
