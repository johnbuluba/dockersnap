import { useState } from "preact/hooks";
import { useInstanceAccess } from "../../lib/queries";
import { useCopy } from "../../lib/copy";
import type { AccessFile } from "../../lib/types";
import { ErrorPanel, Panel, Skeleton } from "../ui";

// Read-only view of the plugin's /access response. Three sections:
//   - Env vars (key=value with copy buttons per row)
//   - Files (collapsed by default; "show" expands the content; copy whole-file)
//   - Endpoints (resolved URL with copy)
//
// No materialization here — `dockersnap use` does that on the CLI side
// because shell-export semantics need to stay there. The dashboard's job
// is to surface what the plugin returned for inspection.
export function AccessTab({ instanceName }: { instanceName: string }) {
  const { data, isLoading, error } = useInstanceAccess(instanceName);
  const { copy, copied } = useCopy();

  if (isLoading) {
    return (
      <Panel>
        <div class="px-4 py-6"><Skeleton class="w-full h-32" /></div>
      </Panel>
    );
  }
  if (error) return <ErrorPanel error={error} />;
  if (!data) return null;

  const hasAny =
    (data.env && Object.keys(data.env).length > 0) ||
    (data.files && data.files.length > 0) ||
    (data.endpoints && data.endpoints.length > 0);

  if (!hasAny) {
    return (
      <Panel>
        <div class="px-4 py-10 text-center text-sm text-text-secondary">
          No access bundle. This instance has no workload, or its plugin's
          access handler returned an empty response.
        </div>
      </Panel>
    );
  }

  return (
    <div class="space-y-3">
      {data.env && Object.keys(data.env).length > 0 && (
        <Panel>
          <SectionHeader title="env" hint="Source from your shell with `eval $(dockersnap use)`." />
          <ul class="divide-y divide-border">
            {Object.entries(data.env).map(([k, v]) => (
              <li
                key={k}
                class="px-3 py-1.5 flex items-start gap-3 text-sm hover:bg-surface-hover"
              >
                <span class="font-mono text-text-secondary shrink-0 w-44">{k}</span>
                <span class="font-mono text-text break-all">{v}</span>
                <CopyButton
                  ariaLabel={`Copy ${k}`}
                  done={copied === `env:${k}`}
                  onClick={() => copy(`${k}=${v}`, `env:${k}`)}
                />
              </li>
            ))}
          </ul>
        </Panel>
      )}

      {data.files && data.files.length > 0 && (
        <Panel>
          <SectionHeader title="files" hint="Materialize via `dockersnap use` on the CLI; or copy here." />
          <ul class="divide-y divide-border">
            {data.files.map((f) => (
              <FileRow key={f.name} file={f} copyState={copied} onCopy={copy} />
            ))}
          </ul>
        </Panel>
      )}

      {data.endpoints && data.endpoints.length > 0 && (
        <Panel>
          <SectionHeader title="endpoints" />
          <ul class="divide-y divide-border">
            {data.endpoints.map((ep) => (
              <li
                key={ep.name}
                class="px-3 py-2 hover:bg-surface-hover space-y-0.5"
              >
                <div class="flex items-center gap-2">
                  <span class="font-mono text-text">{ep.name}</span>
                  {ep.insecure && (
                    <span class="text-xs font-mono uppercase tracking-wide text-status-error">
                      insecure
                    </span>
                  )}
                  {ep.url && (
                    <CopyButton
                      ariaLabel={`Copy ${ep.name} URL`}
                      done={copied === `ep:${ep.name}`}
                      onClick={() => copy(ep.url!, `ep:${ep.name}`)}
                    />
                  )}
                </div>
                {ep.url ? (
                  <a
                    href={ep.url}
                    target="_blank"
                    rel="noreferrer"
                    class="font-mono text-sm text-accent hover:text-accent-hover break-all"
                  >
                    {ep.url}
                  </a>
                ) : (
                  <span class="text-text-muted text-sm">(no URL — port not yet bound)</span>
                )}
                {ep.description && (
                  <p class="text-xs text-text-muted">{ep.description}</p>
                )}
              </li>
            ))}
          </ul>
        </Panel>
      )}
    </div>
  );
}

function SectionHeader({ title, hint }: { title: string; hint?: string }) {
  return (
    <div class="px-3 py-2 border-b border-border flex items-baseline justify-between gap-3">
      <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
        {title}
      </span>
      {hint && <span class="text-xs text-text-muted">{hint}</span>}
    </div>
  );
}

function FileRow({
  file,
  copyState,
  onCopy,
}: {
  file: AccessFile;
  copyState: string | null;
  onCopy: (value: string, key: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const bytes = new Blob([file.content]).size;

  return (
    <li class="px-3 py-1.5 hover:bg-surface-hover">
      <div class="flex items-center gap-3 text-sm">
        <span class="font-mono text-text">{file.name}</span>
        <span class="text-xs text-text-muted font-mono tabular-nums">
          mode={file.mode || "0600"} · {bytes}b
        </span>
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          class="ml-auto text-xs text-text-secondary hover:text-text font-mono"
        >
          {open ? "hide" : "show"}
        </button>
        <CopyButton
          ariaLabel={`Copy ${file.name}`}
          done={copyState === `file:${file.name}`}
          onClick={() => onCopy(file.content, `file:${file.name}`)}
        />
      </div>
      {open && (
        <pre class="mt-2 max-h-72 overflow-auto font-mono text-xs bg-code-bg border border-border rounded-sm px-3 py-2 text-text whitespace-pre">
          {file.content}
        </pre>
      )}
    </li>
  );
}

function CopyButton({
  done,
  onClick,
  ariaLabel,
}: {
  done: boolean;
  onClick: () => void;
  ariaLabel: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={ariaLabel}
      class={`text-xs font-mono px-2 py-0.5 rounded-sm border ${
        done
          ? "border-status-running text-status-running"
          : "border-border text-text-secondary hover:border-border-strong hover:text-text"
      }`}
    >
      {done ? "copied" : "copy"}
    </button>
  );
}
