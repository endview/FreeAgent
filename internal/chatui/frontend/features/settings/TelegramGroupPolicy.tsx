import { useState } from "react";
import { api } from "@/lib/api/client";
import { clearFormDraft } from "@/lib/drafts";
import { Button } from "@/components/ui/button";
import { Loading, Result, useFormDraft, useResource } from "./shared";

interface Target {
  source: string;
  application_id: string;
  chat_id: string;
}
interface State {
  revision: number;
  reply_all: boolean;
  save_background: boolean;
  status?: string;
  can_send: boolean;
  forum: boolean;
  migrated_to?: string;
  migrated_from?: string;
}
export function TelegramGroupPolicy({ target }: { target: Target }) {
  const route = `channels/telegram/groups?bot=${encodeURIComponent(target.application_id)}&chat=${encodeURIComponent(target.chat_id)}`;
  const resource = useResource<{ state: State }>(route);
  return resource.value ? (
    <GroupForm
      target={target}
      state={resource.value.state}
      route={route}
      reload={resource.reload}
    />
  ) : (
    <Loading error={resource.error} />
  );
}
function GroupForm({
  target,
  state,
  route,
  reload,
}: {
  target: Target;
  state: State;
  route: string;
  reload: () => Promise<unknown>;
}) {
  const draftID = `telegram-group:${target.application_id}:${target.chat_id}`;
  const [form, change] = useFormDraft(draftID, {
    revision: state.revision,
    reply_all: state.reply_all,
    save_background: state.save_background,
  });
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  function accept(s: State) {
    change({
      revision: s.revision,
      reply_all: s.reply_all,
      save_background: s.save_background,
    });
    clearFormDraft(draftID);
  }
  async function perform(action: "save" | "clear_background" | "read") {
    setBusy(true);
    setError("");
    setResult("");
    try {
      if (action === "read") {
        const current = await api<{ state: State }>(route);
        accept(current.state);
        setResult("已读取当前群设置。");
      } else {
        if (
          action === "clear_background" &&
          !window.confirm(
            "清空此群临时背景和旧版背景正文？正式请求、运行记录和投递回执保留；备份按原期限到期。",
          )
        )
          return;
        const bytes = await crypto.subtle.digest(
          "SHA-256",
          new TextEncoder().encode(JSON.stringify([target, form, action])),
        );
        const operation_id = Array.from(new Uint8Array(bytes), (n) =>
          n.toString(16).padStart(2, "0"),
        ).join("");
        const saved = await api<{ state?: State }>("channels/telegram/groups", {
          target,
          action,
          reply_all: form.reply_all,
          save_background: form.save_background,
          expected_revision: form.revision,
          operation_id,
        });
        if (saved.state) accept(saved.state);
        setResult(
          action === "save"
            ? "群设置已保存，权限保持原值。"
            : "已清空活动存储中的群背景。",
        );
      }
      await reload();
    } catch (e) {
      setError((e as Error).message);
      await reload().catch(() => {});
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="settings-section">
      <summary>群回复与背景</summary>
      <p className="form-hint">
        本群身份：{state.status || "尚未观察到"} ·{" "}
        {state.can_send ? "未发现禁发状态" : "暂停投递"} ·{" "}
        {state.forum ? "论坛话题" : "普通群"}。平台权限和回执决定能否发送。
      </p>
      {state.migrated_from && (
        <p className="form-hint">
          迁移自群 {state.migrated_from}；旧卡和未核实回复引用不会自动重发。
        </p>
      )}
      <p className="form-hint">
        群空间可读资源等同于可向全群公开。话题隔离上下文，不是成员间的私密空间。
      </p>
      <label className="check-line">
        <input
          type="checkbox"
          checked={form.reply_all}
          onChange={(e) => change({ ...form, reply_all: e.target.checked })}
        />
        全部回复已准入成员（消息成为正式对话，消耗模型额度）
      </label>
      <label className="check-line">
        <input
          type="checkbox"
          checked={form.save_background}
          onChange={(e) =>
            change({ ...form, save_background: e.target.checked })
          }
        />
        保存未唤起的群背景（24 小时，仅文本，不写入 Memory 或长期摘要）
      </label>
      <p className="form-hint">
        两项默认关闭。关闭保存立即清除临时背景；平台隐私模式仍影响 Bot
        能收到哪些消息。
      </p>
      <div className="provider-actions">
        <Button
          type="button"
          disabled={busy}
          onClick={() => void perform("save")}
        >
          保存群设置
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          onClick={() => void perform("read")}
        >
          放弃草稿并读取当前值
        </Button>
        <Button
          type="button"
          variant="outline"
          disabled={busy}
          onClick={() => void perform("clear_background")}
        >
          清空群背景
        </Button>
      </div>
      <Result error={error} text={result} />
    </details>
  );
}
