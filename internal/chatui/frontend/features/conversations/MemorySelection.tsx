import { useState } from "react";
import { api } from "@/lib/api/client";
import { useResource, Loading, Result } from "@/features/settings/shared";
import { Button } from "@/components/ui/button";
type Scope = { kind: string; owner: string; project: string };
type Entry = {
  entry_id: string;
  title?: string;
  scope: Scope;
  revision?: number;
  state?: string;
  pinned?: { scope: string };
};
type Selection = {
  entries: Entry[];
  pins: Entry[];
  knowledge: Scope[];
  task_private: boolean;
};
export function MemorySelection({ chat }: { chat: string }) {
  const resource = useResource<Selection>(
      `chats/${encodeURIComponent(chat)}/memory`,
    ),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function exclude(body: unknown) {
    if (!window.confirm("从本会话后续请求移除此项？已有执行记录保留。")) return;
    setBusy(true);
    setError("");
    try {
      await api(`chats/${encodeURIComponent(chat)}/exclude-memory`, body);
      await resource.reload();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  if (!resource.value) return <Loading error={resource.error} />;
  const value = resource.value;
  return (
    <details className="settings-section">
      <summary>当前 Memory 与固定内容</summary>
      <Result error={error} />
      <ul>
        {(value.entries || []).map((item) => (
          <li key={item.entry_id}>
            {item.title || item.entry_id} · {item.state}
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() =>
                void exclude({
                  entry: { scope: item.scope, entry_id: item.entry_id },
                })
              }
            >
              本会话不再使用
            </Button>
          </li>
        ))}
        {(value.pins || []).map((item) => (
          <li key={item.entry_id}>
            {item.title || item.entry_id} · 固定到 {item.pinned?.scope}
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() => void exclude({ pin_id: item.entry_id })}
            >
              本会话不再带入
            </Button>
          </li>
        ))}
        {(value.knowledge || []).map((scope, index) => (
          <li key={index}>
            知识范围：{scope.owner}
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() => void exclude({ knowledge: scope })}
            >
              本会话不再使用
            </Button>
          </li>
        ))}
      </ul>
      {value.task_private && (
        <p className="form-hint">任务私有 Memory 已开启。</p>
      )}
      {!(
        value.entries?.length ||
        value.pins?.length ||
        value.knowledge?.length
      ) && <p className="form-hint">当前没有额外选入的内容。</p>}
    </details>
  );
}
