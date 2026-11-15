import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { report } from "@/lib/store/runtime";
import { Button } from "@/components/ui/button";

interface Item {
  id: string;
  number: number;
  tool: string;
  resolution?: string;
  note?: string;
  model_note?: string;
  revision: string;
}

export function UnresolvedPanel({
  chat,
  revision,
}: {
  chat: string;
  revision: string;
}) {
  const [items, setItems] = useState<Item[]>([]);
  const [busy, setBusy] = useState(false);
  const [more, setMore] = useState(false);
  async function load(after = 0) {
    const page = await api<Item[]>(
      `attach/unresolved?conversation_id=${encodeURIComponent(chat)}&after=${after}`,
    );
    setItems((old) =>
      after
        ? [
            ...old,
            ...page.filter(
              (item) => !old.some((prior) => prior.id === item.id),
            ),
          ]
        : page,
    );
    setMore(page.length === 50);
  }
  useEffect(() => {
    let disposed = false;
    setItems([]);
    if (chat)
      api<Item[]>(
        `attach/unresolved?conversation_id=${encodeURIComponent(chat)}`,
      )
        .then((page) => {
          if (!disposed) {
            setItems(page);
            setMore(page.length === 50);
          }
        })
        .catch(report);
    return () => {
      disposed = true;
    };
  }, [chat, revision]);
  async function resolve(item: Item, action: string) {
    const label =
      action === "ignored"
        ? "结束提醒"
        : action === "user_confirmed_applied"
          ? "由你确认已生效"
          : "由你确认未生效";
    if (
      !window.confirm(
        `#${item.number}：${label}？这不会重发原操作。忽略仍保留未知事实和重复执行保护。`,
      )
    )
      return;
    setBusy(true);
    try {
      await api("attach/unresolved", {
        conversation_id: chat,
        id: item.id,
        revision: item.revision,
        action,
      });
      await load();
    } catch (error) {
      report(error);
    } finally {
      setBusy(false);
    }
  }
  if (!items.length) return null;
  return (
    <details className="run-evidence">
      <summary>
        未确认事项 · {items.filter((item) => !item.resolution).length} 项待核查
      </summary>
      <p>
        本轮结束不代表这些动作已经成功。原始证据保留；渠道未决及未派发尾段在“渠道投递记录”中处理。
      </p>
      {items.map((item) => (
        <div key={item.id}>
          <p>
            <strong>
              #{item.number} {item.tool}
            </strong>{" "}
            ·{" "}
            {item.resolution === "ignored"
              ? "已结束提醒，效果仍未确认"
              : item.resolution === "not_dispatched"
                ? "原判 UNKNOWN，已核实未执行"
                : item.resolution === "user_confirmed_applied"
                  ? "用户确认已生效"
                  : item.resolution === "user_confirmed_not_applied"
                    ? "用户确认未生效"
                    : "结果未知"}
          </p>
          {item.note && <p>{item.note}</p>}
          {item.model_note && <p>模型判断（非核实证据）：{item.model_note}</p>}
          {!item.resolution && (
            <div className="provider-actions">
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => void resolve(item, "user_confirmed_applied")}
              >
                确认已生效
              </Button>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => void resolve(item, "user_confirmed_not_applied")}
              >
                确认未生效
              </Button>
              <Button
                variant="ghost"
                disabled={busy}
                onClick={() => void resolve(item, "ignored")}
              >
                结束提醒
              </Button>
            </div>
          )}
        </div>
      ))}
      {more && (
        <Button
          variant="outline"
          disabled={busy}
          onClick={() => void load(items.at(-1)?.number).catch(report)}
        >
          加载更多
        </Button>
      )}
    </details>
  );
}
