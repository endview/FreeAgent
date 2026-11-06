import { useState } from "react";
import { api, remote } from "@/lib/api/client";
import { useRuntime } from "@/lib/store/runtime";
import { clearFormDraft } from "@/lib/drafts";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { Field, Loading, Result, useFormDraft, useResource } from "./shared";

interface Memory {
  id: string;
  title: string;
  body: string;
  revision: number;
  state: string;
  scope: { kind: string; owner?: string; project?: boolean };
  pinned?: { scope: string };
}
export default function MemorySettings() {
  const state = useRuntime();
  if (!state.selected)
    return (
      <p className="settings-intro">
        先打开一个对话，以确定 Memory 的归属和固定范围。
      </p>
    );
  return remote ? (
    <MemorySelection chat={state.selected} />
  ) : (
    <MemoryEditor key={state.selected} chat={state.selected} />
  );
}
function MemorySelection({ chat }: { chat: string }) {
  const resource = useResource<{ entries: unknown[]; pins: unknown[] }>(
    `chats/${encodeURIComponent(chat)}/memory`,
  );
  return (
    <>
      <p className="settings-intro">
        当前对话选择的 Memory 与固定内容。公网管理不开放本机 attach
        接口；持久修改可通过对话提案确认或本机设置完成。
      </p>
      {resource.value ? (
        <pre className="settings-output">
          {JSON.stringify(resource.value, null, 2)}
        </pre>
      ) : (
        <Loading error={resource.error} />
      )}
    </>
  );
}
function MemoryEditor({ chat }: { chat: string }) {
  const [entries, setEntries] = useState<Memory[] | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [pinScope, setPinScope] = useState("conversation");
  const [form, change] = useFormDraft("memory:" + chat, {
    title: "",
    body: "",
  });
  async function read() {
    setEntries(
      await api<Memory[]>("attach/memory", {
        command: "list",
        conversation_id: chat,
        scope_kind: "private",
        limit: 64,
      }),
    );
  }
  async function perform(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
      await read();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <p className="settings-intro">
        Memory 独立保存。固定内容按范围加入后续请求，每条最多 2 KiB，合计最多 8
        KiB；占用最终由 Core 核算。
      </p>
      <Button
        variant="outline"
        disabled={busy}
        onClick={() => void perform(read)}
      >
        读取当前对话 Memory
      </Button>
      {entries?.map((item) => (
        <article className="settings-section" key={item.id}>
          <h2>{item.title}</h2>
          <p className="form-hint">
            版本 {item.revision} · {item.state}
            {item.pinned ? ` · 固定到 ${item.pinned.scope}` : ""}
          </p>
          <details>
            <summary>查看内容</summary>
            <pre className="settings-output">{item.body}</pre>
          </details>
          <div className="provider-actions">
            {[
              [
                item.pinned ? "unpin" : "pin",
                item.pinned ? "取消固定" : "固定",
              ],
              [
                item.state === "active" ? "disable" : "enable",
                item.state === "active" ? "停用" : "启用",
              ],
              ["delete", "删除"],
            ].map(([command, label]) => (
              <Button
                variant="ghost"
                disabled={busy}
                key={command}
                onClick={() => {
                  if (
                    window.confirm(
                      `${label}“${item.title}”，版本 ${item.revision}${command === "pin" ? `，范围 ${pinScope}` : ""}？`,
                    )
                  )
                    void perform(async () => {
                      await api("attach/memory", {
                        command,
                        conversation_id: chat,
                        scope_kind: item.scope.kind,
                        owner: item.scope.owner,
                        personal: !item.scope.project,
                        entries: [item.id],
                        expected_revision: item.revision,
                        pin_scope: pinScope,
                        yes: true,
                        request_key: crypto.randomUUID(),
                      });
                    });
                }}
              >
                {label}
              </Button>
            ))}
          </div>
        </article>
      ))}
      <Field label="固定范围">
        <Choice
          label="固定范围"
          value={pinScope}
          options={[
            ["conversation", "当前对话"],
            ["workspace", "当前工作区"],
            ["global", "全局"],
          ]}
          onChange={setPinScope}
        />
      </Field>
      <form
        className="settings-section"
        onSubmit={(event) => {
          event.preventDefault();
          if (
            !window.confirm(
              `保存“${form.title}”到当前对话 Memory？保存不会自动固定。`,
            )
          )
            return;
          void perform(async () => {
            await api("attach/memory", {
              command: "save",
              conversation_id: chat,
              scope_kind: "private",
              kind: "background",
              title: form.title,
              body: form.body,
              yes: true,
              request_key: crypto.randomUUID(),
            });
            change({ title: "", body: "" });
            clearFormDraft("memory:" + chat);
          });
        }}
      >
        <h2>保存 Memory</h2>
        <fieldset disabled={busy}>
          <Field label="Memory 标题">
            <input
              required
              maxLength={256}
              value={form.title}
              onChange={(event) =>
                change({ ...form, title: event.target.value })
              }
            />
          </Field>
          <Field label="Memory 正文">
            <textarea
              required
              rows={6}
              maxLength={65536}
              value={form.body}
              onChange={(event) =>
                change({ ...form, body: event.target.value })
              }
            />
          </Field>
          <Button type="submit">确认保存 Memory</Button>
        </fieldset>
      </form>
      <Result error={error} />
    </>
  );
}
