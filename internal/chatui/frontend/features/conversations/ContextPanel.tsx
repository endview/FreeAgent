import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import type { ContextState } from "@/lib/api/types";
import { Button } from "@/components/ui/button";
import { Field, Result } from "@/features/settings/shared";
import { MemorySelection } from "./MemorySelection";
import {
  contextActions,
  contextStatus,
  summaryByteLimit,
} from "../../lib/presentation/context.mjs";

export function ContextPanel({
  chat,
  onDone,
}: {
  chat: string;
  onDone: () => void;
}) {
  const [state, setState] = useState<ContextState | null>(null),
    [bytes, setBytes] = useState(4096);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState(""),
    [result, setResult] = useState("");
  const [requestKey, setRequestKey] = useState("");
  useEffect(() => {
    let disposed = false;
    api<ContextState>(
      `attach/context?conversation_id=${encodeURIComponent(chat)}`,
    )
      .then((value) => {
        if (!disposed) {
          setState(value);
          setBytes(value.byte_limit || 4096);
        }
      })
      .catch((error) => {
        if (!disposed) setError(error.message);
      });
    return () => {
      disposed = true;
    };
  }, [chat]);
  const allowed = contextActions(state, busy);
  async function action(action: string) {
    if (!state) return;
    if (
      action === "clear" &&
      !window.confirm("清除当前上下文摘要？原始对话和实际执行记录保留。")
    )
      return;
    let limit: number;
    try {
      limit = summaryByteLimit(bytes);
    } catch (error) {
      setError((error as Error).message);
      return;
    }
    const key = crypto.randomUUID();
    setRequestKey(key);
    setBusy(true);
    setError("");
    setResult("");
    try {
      const value = await api<{
        state: ContextState;
        changed: boolean;
        model_calls: number;
        before_bytes: number;
        after_bytes: number;
      }>("attach/context", {
        conversation_id: chat,
        request_key: key,
        command: {
          action,
          expected_revision: state.revision,
          ...(action !== "clear" ? { summary_bytes: limit } : {}),
        },
      });
      setState(value.state);
      setResult(
        `${value.changed ? "已更新" : "无需更新"} · 模型调用 ${value.model_calls} 次 · ${value.before_bytes} → ${value.after_bytes} 字节，原始记录保留。`,
      );
    } catch (error) {
      setError(
        error instanceof Error ? error.message : "压缩未完成，请核查原记录。",
      );
      try {
        setState(
          await api<ContextState>(
            `attach/context?conversation_id=${encodeURIComponent(chat)}`,
          ),
        );
      } catch {}
    } finally {
      setBusy(false);
      setRequestKey("");
      onDone();
    }
  }
  return (
    <section className="context-panel">
      <h2>上下文</h2>
      <p>{busy ? "正在压缩上下文…" : contextStatus(state)}</p>
      {state && (
        <>
          <p className="form-hint">
            {state.automatic ? "自动压缩已开启" : "自动压缩已关闭"} · 触发比例{" "}
            {state.window.automatic_percent}%
          </p>
          {state.automatic_paused && (
            <p className="inline-warning">{state.automatic_paused}</p>
          )}
          <dl className="context-metrics">
            <div>
              <dt>模型窗口</dt>
              <dd>
                {state.window.tokens
                  ? state.window.tokens.toLocaleString() + " tokens"
                  : "尚未配置，按字节限额管理"}
              </dd>
            </div>
            <div>
              <dt>输出预留</dt>
              <dd>
                {state.window.output_reserve_tokens.toLocaleString()} tokens
              </dd>
            </div>
            <div>
              <dt>当前占用估算</dt>
              <dd>
                {state.last_request
                  ? `${state.last_request.occupancy_percent}% · ${state.last_request.estimated_input_tokens.toLocaleString()} tokens / ${state.last_request.bytes.toLocaleString()} 字节`
                  : "下一次请求后显示"}
              </dd>
            </div>
          </dl>
          {state.last_request?.newer_history_available && (
            <p className="form-hint">
              这是上一条实际请求的估算，之后的消息尚未计入。
            </p>
          )}
          {state.last_request?.parts && (
            <details>
              <summary>占用来源</summary>
              <ul>
                {state.last_request.parts.map((part, index) => (
                  <li key={index}>
                    {part.kind}：{part.bytes.toLocaleString()} 字节 / 估算{" "}
                    {part.estimated_tokens.toLocaleString()} tokens
                  </li>
                ))}
              </ul>
            </details>
          )}
          <Field label="摘要上限（字节）">
            <input
              type="number"
              min={1}
              max={16384}
              step={1}
              disabled={busy}
              value={bytes}
              onChange={(event) => setBytes(Number(event.target.value))}
            />
          </Field>
          {state.summary && (
            <details>
              <summary>查看当前摘要</summary>
              <pre>{state.summary.text}</pre>
            </details>
          )}
          {state.blocked && (
            <p className="inline-warning">
              存在运行中请求、待确认操作或未知结果，请先处理该任务。
            </p>
          )}
        </>
      )}
      <MemorySelection chat={chat} />
      <Result error={error} text={result} />
      <div className="provider-actions">
        <Button disabled={!allowed.compact} onClick={() => action("compact")}>
          手动压缩
        </Button>
        <Button
          variant="outline"
          disabled={!allowed.refresh}
          onClick={() => action("refresh")}
        >
          重新生成摘要
        </Button>
        <Button
          variant="ghost"
          disabled={!allowed.clear}
          onClick={() => action("clear")}
        >
          清除摘要
        </Button>
        {busy && (
          <Button
            variant="outline"
            onClick={() =>
              api("attach/cancel", {
                conversation_id: chat,
                binding: requestKey,
              }).catch((error) => setError(error.message))
            }
          >
            停止压缩
          </Button>
        )}
      </div>
    </section>
  );
}
