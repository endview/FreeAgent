import { Popover } from "radix-ui";
import { WandSparkles } from "lucide-react";
import type { CapabilityCatalog, CapabilitySelection } from "@/lib/api/types";
import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { characterSelection } from "@/lib/presentation/capabilities.mjs";

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
        <span>
          能力
          {(selection.skills?.length || 0) +
          (selection.mcp_servers?.length || 0) +
          (selection.extensions?.length || 0)
            ? ` · ${(selection.skills?.length || 0) + (selection.mcp_servers?.length || 0) + (selection.extensions?.length || 0)}`
            : ""}
        </span>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          className="capability-popover"
          sideOffset={10}
          collisionPadding={12}
          align="end"
        >
          <h2>对话能力</h2>
          <p>
            {busy
              ? "当前执行保持原快照；选择用于后续任务。"
              : conversationID
                ? "勾选后自动保存到此对话，后续消息沿用。"
                : "选择本次新对话允许使用的能力。"}
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
                      {("name" in item && item.name) || item.id}
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
  const [defaultID, setDefaultID] = useState("");
  const [loaded, setLoaded] = useState(false);
  const id = "cc.endview.affectweave";
  useEffect(() => {
    const controller = new AbortController();
    setLoaded(false);
    setError("");
    Promise.all([
      api<{ choices: { id: string; label: string }[] }>(
        `extensions/${id}/state?conversation_id=${encodeURIComponent(conversationID)}`,
        undefined,
        undefined,
        controller.signal,
      ),
      api<{ settings?: { role_id?: string } }>(
        `extensions/${id}/view`,
        undefined,
        undefined,
        controller.signal,
      ),
    ])
      .then(([r, config]) => {
        if (!controller.signal.aborted) {
          setChoices(r.choices || []);
          setDefaultID(config.settings?.role_id || "");
          setLoaded(true);
        }
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      });
    return () => controller.abort();
  }, [conversationID]);
  const effective = characterSelection(
    choices,
    selection.extension_variants?.[id],
    defaultID,
  );
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
          <option value="">
            {!loaded
              ? "正在读取默认人物…"
              : defaultID
                ? `使用扩展默认：${choices.find((c) => c.id === defaultID)?.label || "此空间不可用"}`
                : "未设置默认人物（不会注入）"}
          </option>
          {choices.map((c) => (
            <option key={c.id} value={c.id}>
              {c.label}
            </option>
          ))}
        </select>
      </label>
      {loaded && (
        <p className={effective.ready ? "form-hint" : "inline-warning"}>
          {effective.ready
            ? `后续消息使用「${effective.label}」。人物选择自动保存在此对话。`
            : "扩展已选中，但当前没有可用人物。请在上方选择，或在扩展设置中配置默认人物。"}
        </p>
      )}
      {error && <p className="inline-error">{error}</p>}
    </section>
  );
}
