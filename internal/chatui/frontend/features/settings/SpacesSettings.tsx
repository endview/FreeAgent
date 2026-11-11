import { useState } from "react";
import { api } from "@/lib/api/client";
import { loadNavigation, loadSnapshot, useRuntime } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { Field, Result } from "./shared";
import type { Space } from "@/lib/api/types";

export default function SpacesSettings() {
  const state = useRuntime();
  const [title, setTitle] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function change(kind: string, args: Record<string, unknown>) {
    setBusy(true);
    setError("");
    try {
      await api("spaces", { kind, arguments: args, confirmed: true });
      await loadNavigation();
      await loadSnapshot();
      return true;
    } catch (error) {
      setError((error as Error).message);
      return false;
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <p className="settings-intro">
        空间就是会话归类文件夹。外部聊天默认复用第一条对话；只有明确开启后才使用多对话。
      </p>
      <form
        className="inline-form"
        onSubmit={(event) => {
          event.preventDefault();
          void change("space.create", { title }).then((saved) => {
            if (saved) setTitle("");
          });
        }}
      >
        <Field label="新空间名称">
          <input
            required
            maxLength={80}
            value={title}
            onChange={(event) => setTitle(event.target.value)}
          />
        </Field>
        <Button type="submit" variant="outline" disabled={busy}>
          创建空间
        </Button>
      </form>
      <Result error={error} />
      {state.spaces.map((space) => (
        <SpaceEditor
          key={space.id}
          space={space}
          chat={state.selected}
          busy={busy}
          onChange={change}
        />
      ))}
    </>
  );
}
function SpaceEditor({
  space,
  chat,
  busy,
  onChange,
}: {
  space: Space;
  chat: string;
  busy: boolean;
  onChange: (kind: string, args: Record<string, unknown>) => Promise<boolean>;
}) {
  const [title, setTitle] = useState(space.title),
    [mode, setMode] = useState<string>(space.permission || "none");
  return (
    <section className="settings-section">
      <h2>{space.title}</h2>
      <p className="form-hint">
        {space.source} · {space.permission || "none"}
        {space.multi_conversation_enabled ? " · 多对话已开启" : " · 单对话"}
      </p>
      <Field label={`${space.title} 名称`}>
        <input
          value={title}
          onChange={(event) => setTitle(event.target.value)}
        />
      </Field>
      <div className="provider-actions">
        <Button
          variant="outline"
          disabled={busy || !title.trim()}
          onClick={() =>
            onChange("space.rename", { space_id: space.id, title })
          }
        >
          重命名
        </Button>
        {space.id !== "local" && (
          <Button
            variant="ghost"
            disabled={busy}
            onClick={() => {
              if (
                window.confirm(
                  "删除空间并把对话移回本地？外部聊天会解除关联，历史保留。",
                )
              )
                void onChange("space.delete", { space_id: space.id });
            }}
          >
            删除空间
          </Button>
        )}
      </div>
      <Field label={`${space.title} 权限`}>
        <Choice
          label={`${space.title} 权限`}
          value={mode}
          options={[
            ["none", "仅聊天"],
            ["ask", "每次询问"],
            ["workspace", "工作区"],
            ["full", "Full access"],
          ]}
          onChange={setMode}
        />
      </Field>
      <Button
        variant="outline"
        disabled={busy}
        onClick={() => {
          if (
            window.confirm(`更改整个空间的权限为 ${mode}？影响空间内所有对话。`)
          )
            void onChange("space.set_permission", { space_id: space.id, mode });
        }}
      >
        保存空间权限
      </Button>
      <label className="check-line">
        <input
          type="checkbox"
          checked={Boolean(space.owner_private)}
          disabled={busy}
          onChange={(event) => {
            const owner_private = event.target.checked;
            if (
              window.confirm(
                owner_private
                  ? "确认这是本人使用的私聊空间，并授予私人默认上下文和能力？"
                  : "取消此空间的私人默认上下文授权？",
              )
            )
              void onChange("space.set_permission", {
                space_id: space.id,
                selection: {
                  ...space.selection,
                  mode: space.permission || "none",
                  owner_private,
                },
              });
          }}
        />
        Owner 私聊空间（私人默认上下文）
      </label>
      {space.source !== "local" && (
        <label className="check-line">
          <input
            type="checkbox"
            disabled={busy}
            checked={Boolean(space.multi_conversation_enabled)}
            onChange={(event) => {
              if (
                window.confirm(
                  event.target.checked
                    ? "开启外部聊天的多对话功能？"
                    : "关闭多对话并使用默认对话？",
                )
              )
                void onChange("space.set_multi", {
                  space_id: space.id,
                  enabled: event.target.checked,
                });
            }}
          />
          开启多对话
        </label>
      )}
      {chat && (
        <div className="provider-actions">
          <Button
            variant="ghost"
            disabled={busy}
            onClick={() => {
              if (
                window.confirm(
                  `把当前对话移动到“${space.title}”？${space.source !== "local" ? "外部空间内可访问此对话的人员将能看到内容。" : ""}`,
                )
              )
                void onChange("space.move_conversation", {
                  space_id: space.id,
                  conversation_id: chat,
                });
            }}
          >
            把当前对话移入此空间
          </Button>
          {space.source !== "local" && (
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() =>
                onChange("space.set_default", {
                  space_id: space.id,
                  conversation_id: chat,
                })
              }
            >
              将当前对话设为默认
            </Button>
          )}
          {space.source !== "local" && (
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() => {
                if (
                  window.confirm("将当前对话设为此外部空间接下来使用的对话？")
                )
                  void onChange("space.set_active", {
                    space_id: space.id,
                    conversation_id: chat,
                  });
              }}
            >
              切换外部当前对话
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
