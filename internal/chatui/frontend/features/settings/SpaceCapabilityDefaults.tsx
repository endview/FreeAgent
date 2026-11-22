import { useState } from "react";
import { api } from "@/lib/api/client";
import { clearFormDraft } from "@/lib/drafts";
import type { CapabilityCatalog, CapabilitySelection } from "@/lib/api/types";
import { Button } from "@/components/ui/button";
import { Loading, Result, useFormDraft, useResource } from "./shared";

interface Page {
  state: { selection: CapabilitySelection; configured: boolean };
  catalog: CapabilityCatalog;
  expected_revisions: Record<string, number>;
}
export function SpaceCapabilityDefaults({ space }: { space: string }) {
  const [open, setOpen] = useState(false);
  const route = `spaces/capabilities?space=${encodeURIComponent(space)}`;
  const resource = useResource<Page>(route, open);
  return (
    <details onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>新会话的默认能力</summary>
      <p className="form-hint">
        仅新会话生效。已有选择和权限不变。群空间可读资料视为对全群公开。
      </p>
      {resource.value ? (
        <DefaultsForm
          space={space}
          page={resource.value}
          route={route}
          reload={resource.reload}
        />
      ) : open ? (
        <Loading error={resource.error} />
      ) : null}
    </details>
  );
}
function DefaultsForm({
  space,
  page,
  route,
  reload,
}: {
  space: string;
  page: Page;
  route: string;
  reload: () => Promise<unknown>;
}) {
  const draftID = `space-default-capabilities:${space}`;
  const [form, change] = useFormDraft(draftID, {
    selection: page.state.selection,
    expected: page.expected_revisions,
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  function accept(value: Page) {
    change({
      selection: value.state.selection,
      expected: value.expected_revisions,
    });
    clearFormDraft(draftID);
  }
  async function perform(save: boolean) {
    setBusy(true);
    setError("");
    setResult("");
    try {
      if (save)
        await api("spaces/capabilities", {
          space_id: space,
          selection: form.selection,
          expected_revisions: form.expected,
        });
      const latest = await api<Page>(route);
      accept(latest);
      await reload();
      setResult(
        save ? "空间默认能力已保存；已有会话不变。" : "已读取当前默认能力。",
      );
    } catch (e) {
      setError((e as Error).message);
      await reload().catch(() => {});
    } finally {
      setBusy(false);
    }
  }
  function toggle(
    kind: "skills" | "mcp_servers" | "extensions",
    id: string,
    enabled: boolean,
  ) {
    const selection = {
      ...form.selection,
      [kind]: enabled
        ? [...(form.selection[kind] || []), id]
        : (form.selection[kind] || []).filter((value) => value !== id),
    };
    if (kind === "extensions" && !enabled) {
      selection.extension_variants = { ...selection.extension_variants };
      selection.extension_settings = { ...selection.extension_settings };
      delete selection.extension_variants[id];
      delete selection.extension_settings[id];
    }
    change({ ...form, selection });
  }
  function options(kind: "skills" | "mcp_servers" | "extensions") {
    const items = (page.catalog[kind] || []).map((item) => ({
      id: item.id,
      enabled: item.enabled,
      name: ("name" in item && item.name) || item.id,
    }));
    for (const id of form.selection[kind] || [])
      if (!items.some((item) => item.id === id))
        items.push({ id, enabled: false, name: id });
    return items;
  }
  return (
    <div>
      {(
        [
          ["skills", "Skill"],
          ["mcp_servers", "MCP"],
          ["extensions", "扩展"],
        ] as const
      ).map(([kind, label]) => (
        <fieldset key={kind} className="capability-group">
          <legend>{label}</legend>
          {options(kind).length ? (
            options(kind).map((item) => (
              <label key={item.id} className="check-line">
                <input
                  type="checkbox"
                  checked={(form.selection[kind] || []).includes(item.id)}
                  disabled={
                    busy ||
                    (!item.enabled &&
                      !(form.selection[kind] || []).includes(item.id))
                  }
                  onChange={(e) => toggle(kind, item.id, e.target.checked)}
                />
                {("name" in item && item.name) || item.id}
                {!item.enabled ? "（未启用）" : ""}
              </label>
            ))
          ) : (
            <p className="form-hint">没有已配置的{label}；纯 Core 仍可聊天。</p>
          )}
        </fieldset>
      ))}
      <div className="provider-actions">
        <Button
          type="button"
          disabled={busy}
          onClick={() => void perform(true)}
        >
          保存空间默认能力
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          onClick={() => void perform(false)}
        >
          放弃草稿并读取默认能力
        </Button>
      </div>
      <Result error={error} text={result} />
    </div>
  );
}
