import { useState } from "preact/hooks";
import { Modal, ModalFooter } from "../Modal";
import { ProgressLog } from "../ProgressLog";
import { useStreamingMutation } from "../../lib/stream";
import { usePlugins } from "../../lib/queries";
import type { ConfigOption } from "../../lib/types";

// Create-instance modal. Two modes:
//   1. Plain Docker (no plugin) — just a name field.
//   2. With a plugin — name + plugin dropdown + dynamic config rows
//      derived from that plugin's ConfigOptions.
//
// Submit POSTs /api/v1/instances with { name, workload_inline?: {...} }
// and streams progress.
export function CreateModal({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const plugins = usePlugins();

  const [name, setName] = useState("");
  const [pluginName, setPluginName] = useState("");
  // Stored as flat string map; coerced at submit time per ConfigOption.type.
  const [config, setConfig] = useState<Record<string, string>>({});

  const selected = plugins.data?.find((p) => p.name === pluginName);

  const buildBody = () => {
    const body: Record<string, unknown> = { name: name.trim() };
    if (pluginName) {
      const coerced: Record<string, unknown> = {};
      for (const opt of selected?.config_options ?? []) {
        const raw = config[opt.name];
        if (raw === undefined || raw === "") continue;
        coerced[opt.name] = coerceConfigValue(raw, opt.type);
      }
      body.workload_inline = { plugin: pluginName, config: coerced };
    }
    return body;
  };

  const m = useStreamingMutation({
    method: "POST",
    path: "/api/v1/instances",
    body: buildBody(),
    invalidate: [["instances"], ["health"]],
  });

  const close = () => {
    if (m.status === "pending") return; // don't close mid-stream
    m.reset();
    setName("");
    setPluginName("");
    setConfig({});
    onClose();
  };

  return (
    <Modal open={open} title="Create instance" onClose={close}>
      {m.status === "idle" && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!name.trim()) return;
            m.start();
          }}
          class="space-y-3"
        >
          <Field label="name">
            <input
              autoFocus
              type="text"
              value={name}
              onInput={(e) => setName((e.target as HTMLInputElement).value)}
              placeholder="dev"
              pattern="[a-z][a-z0-9-]*"
              maxLength={32}
              required
              class="w-full font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none"
            />
            <p class="text-xs text-text-muted mt-1">
              lowercase, digits, hyphens — must start with a letter.
            </p>
          </Field>

          <Field label="plugin (optional)">
            <select
              value={pluginName}
              onChange={(e) => {
                setPluginName((e.target as HTMLSelectElement).value);
                setConfig({});
              }}
              class="w-full font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text"
            >
              <option value="">— plain Docker —</option>
              {plugins.data
                ?.filter((p) => p.status === "ready")
                .map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name} {p.version && `(${p.version})`}
                  </option>
                ))}
            </select>
            {selected?.description && (
              <p class="text-xs text-text-secondary mt-1">{selected.description}</p>
            )}
          </Field>

          {selected && (selected.config_options?.length ?? 0) > 0 && (
            <div class="space-y-2 border-l border-border pl-3 ml-1">
              <p class="text-xs uppercase tracking-wide text-text-muted font-mono">
                {selected.name} config
              </p>
              {selected.config_options!.map((opt) => (
                <ConfigInput
                  key={opt.name}
                  opt={opt}
                  value={config[opt.name] ?? ""}
                  onChange={(v) =>
                    setConfig((c) => ({ ...c, [opt.name]: v }))
                  }
                />
              ))}
            </div>
          )}

          <ModalFooter>
            <button
              type="button"
              onClick={close}
              class="text-text-secondary hover:text-text px-3 py-1.5 rounded-sm text-sm"
            >
              Cancel
            </button>
            <button
              type="submit"
              class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
            >
              Create
            </button>
          </ModalFooter>
        </form>
      )}

      {m.status !== "idle" && (
        <div class="space-y-3">
          <ProgressLog events={m.events} status={m.status} error={m.error} />
          <ModalFooter>
            {m.status === "done" && (
              <button
                type="button"
                onClick={close}
                class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
              >
                Close
              </button>
            )}
            {m.status === "error" && (
              <>
                <button
                  type="button"
                  onClick={close}
                  class="text-text-secondary hover:text-text px-3 py-1.5 rounded-sm text-sm"
                >
                  Close
                </button>
                <button
                  type="button"
                  onClick={() => m.start()}
                  class="bg-accent text-accent-fg hover:bg-accent-hover px-3 py-1.5 rounded-sm text-sm font-medium"
                >
                  Retry
                </button>
              </>
            )}
            {m.status === "pending" && (
              <span class="text-xs text-text-muted font-mono">streaming…</span>
            )}
          </ModalFooter>
        </div>
      )}
    </Modal>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: preact.ComponentChildren;
}) {
  return (
    <label class="block space-y-1">
      <span class="text-xs uppercase tracking-wide text-text-muted font-mono">
        {label}
      </span>
      {children}
    </label>
  );
}

function ConfigInput({
  opt,
  value,
  onChange,
}: {
  opt: ConfigOption;
  value: string;
  onChange: (v: string) => void;
}) {
  if (opt.type === "bool") {
    const checked = value === "true";
    return (
      <label class="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={checked}
          onChange={(e) =>
            onChange((e.target as HTMLInputElement).checked ? "true" : "false")
          }
          class="size-3.5 accent-accent"
        />
        <span class="font-mono text-xs text-text-secondary">{opt.name}</span>
        {opt.description && (
          <span class="text-xs text-text-muted">— {opt.description}</span>
        )}
      </label>
    );
  }
  return (
    <label class="block space-y-0.5">
      <span class="font-mono text-xs text-text-secondary">
        {opt.name}{" "}
        <span class="text-text-muted">{opt.type}</span>
        {opt.default !== undefined && (
          <span class="text-text-muted"> · default: {String(opt.default)}</span>
        )}
      </span>
      <input
        type={opt.type === "int" ? "number" : "text"}
        value={value}
        onInput={(e) => onChange((e.target as HTMLInputElement).value)}
        placeholder={String(opt.default ?? "")}
        class="w-full font-mono text-sm bg-bg border border-border rounded-sm px-2 py-1 text-text placeholder:text-text-muted focus-visible:border-accent focus-visible:outline-none"
      />
      {opt.description && (
        <span class="text-xs text-text-muted block">{opt.description}</span>
      )}
    </label>
  );
}

function coerceConfigValue(raw: string, type: ConfigOption["type"]): unknown {
  switch (type) {
    case "bool":
      return raw === "true";
    case "int": {
      const n = Number(raw);
      return Number.isFinite(n) ? Math.floor(n) : raw;
    }
    case "string-list":
      return raw.split(",").map((s) => s.trim()).filter(Boolean);
    case "path":
    case "string":
    default:
      return raw;
  }
}
