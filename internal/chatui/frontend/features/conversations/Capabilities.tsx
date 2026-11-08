import { Popover } from "radix-ui";
import { WandSparkles } from "lucide-react";
import type { CapabilityCatalog, CapabilitySelection } from "@/lib/api/types";
import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";

const labels = { skills: "Skill", mcp_servers: "MCP", extensions: "扩展" };
export function Capabilities({
  catalog,
  selection,
  onChange,
  busy = false,
  saving = false,
  conversationID,
}: {
  catalog?: CapabilityCatalog;
  selection: CapabilitySelection;
  onChange: (next: CapabilitySelection) => void;
  busy?: boolean;
  saving?: boolean;
  conversationID?: string;
}) {
  return (
    <Popover.Root>
      <Popover.Trigger aria-label="本轮能力" className="capabilities-button">
        <WandSparkles size={15} />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          className="capability-popover"
          sideOffset={10}
          align="end"
        >
          <h2>本轮能力</h2>
          <p>
            {busy
              ? "当前执行保持原快照；选择用于后续任务。"
              : "选择本轮允许使用的能力。"}
          </p>
          {(Object.keys(labels) as (keyof typeof labels)[]).map((group) => (
            <section key={group}>
              <h3>{labels[group]}</h3>
              {catalog?.[group]?.length ? (
                catalog[group].map((item) => (
                  <label className="check-line" key={item.id}>
                    <input
                      type="checkbox"
                      disabled={!item.enabled || saving}
                      checked={Boolean(selection[group]?.includes(item.id))}
                      onChange={(event) => {
                        const values = new Set(selection[group] || []);
                        if (event.target.checked) values.add(item.id);
                        else values.delete(item.id);
                        const variants = { ...selection.extension_variants };
                        const settings = { ...selection.extension_settings };
                        if (group === "extensions" && !event.target.checked) {
                          delete variants[item.id];
                          delete settings[item.id];
                        }
                        onChange({
                          ...selection,
                          [group]: [...values].sort(),
                          extension_variants: variants,
                          extension_settings: settings,
                        });
                      }}
                    />
                    <span>
                      {item.id}
                      {!item.enabled ? "（全局未启用）" : ""}
                    </span>
                  </label>
                ))
              ) : (
                <p>尚未配置</p>
              )}
            </section>
          ))}
          {conversationID &&
            catalog?.extensions.some(
              (item) => item.id === "cc.endview.affectweave" && item.enabled,
            ) &&
            selection.extensions?.includes("cc.endview.affectweave") && (
              <VariantPicker
                conversationID={conversationID}
                selection={selection}
                onChange={onChange}
                disabled={saving}
              />
            )}
          <Popover.Arrow className="popover-arrow" />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

export function ExtensionTurnChoices({
  catalog,
  selection,
  onChange,
}: {
  catalog?: CapabilityCatalog;
  selection: CapabilitySelection;
  onChange: (next: CapabilitySelection) => void;
}) {
  return catalog?.extensions
    .filter((item) => item.enabled && selection.extensions?.includes(item.id))
    .flatMap((item) =>
      (item.turn_settings || []).map((field) => (
        <label className="setting-control" key={`${item.id}:${field.key}`}>
          <span>
            {item.name || item.id} · {field.label}
          </span>
          <select
            aria-label={`${item.name || item.id}：${field.label}`}
            value={selection.extension_settings?.[item.id]?.[field.key] || ""}
            onChange={(event) => {
              const settings = { ...selection.extension_settings };
              const values = { ...settings[item.id] };
              if (event.target.value) values[field.key] = event.target.value;
              else delete values[field.key];
              if (Object.keys(values).length) settings[item.id] = values;
              else delete settings[item.id];
              onChange({ ...selection, extension_settings: settings });
            }}
          >
            <option value="">继承扩展设置</option>
            {field.options.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
      )),
    );
}

function VariantPicker({
  conversationID,
  selection,
  onChange,
  disabled = false,
}: {
  conversationID: string;
  selection: CapabilitySelection;
  onChange: (next: CapabilitySelection) => void;
  disabled?: boolean;
}) {
  const [choices, setChoices] = useState<{ id: string; label: string }[]>([]),
    [error, setError] = useState("");
  const id = "cc.endview.affectweave";
  useEffect(() => {
    const controller = new AbortController();
    api<{ choices: { id: string; label: string }[] }>(
      `extensions/${id}/state?conversation_id=${encodeURIComponent(conversationID)}`,
      undefined,
      undefined,
      controller.signal,
    )
      .then((r) => setChoices(r.choices || []))
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      });
    return () => controller.abort();
  }, [conversationID]);
  return (
    <section>
      <h3>AffectWeave 人物</h3>
      <label className="setting-control">
        <span>本轮人物</span>
        <select
          disabled={disabled}
          value={selection.extension_variants?.[id] || ""}
          onChange={(e) => {
            const variants = { ...selection.extension_variants };
            if (e.target.value) variants[id] = e.target.value;
            else delete variants[id];
            onChange({ ...selection, extension_variants: variants });
          }}
        >
          <option value="">使用扩展默认人物</option>
          {choices.map((c) => (
            <option key={c.id} value={c.id}>
              {c.label}
            </option>
          ))}
        </select>
      </label>
      {error && <p className="inline-error">{error}</p>}
    </section>
  );
}
