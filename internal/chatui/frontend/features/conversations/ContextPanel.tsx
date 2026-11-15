import { useEffect, useState, useRef } from "react";
import { useRuntime } from "@/lib/store/runtime";
import { api } from "@/lib/api/client";
import type { ContextState } from "@/lib/api/types";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import { Field, Result } from "@/features/settings/shared";
import { MemorySelection } from "./MemorySelection";
import {
  contextActions,
  contextStatus,
  summaryByteLimit,
  contextScopeLabel,
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
  const [targetMode, setTargetMode] = useState("manual");
  const { contextRevision } = useRuntime();
  const editing = useRef(false),
    openedChat = useRef(chat);
  const draftRevisions = useRef<Record<string, number> | undefined>(undefined);
  useEffect(() => {
    let disposed = false;
    if (openedChat.current !== chat) {
      openedChat.current = chat;
      editing.current = false;
    }
    api<ContextState>(
      `attach/context?conversation_id=${encodeURIComponent(chat)}`,
    )
      .then((value) => {
        if (!disposed) {
          setState(value);
          if (!editing.current) {
            draftRevisions.current = value.expected_revisions;
            setBytes(value.byte_limit || 4096);
            setTargetMode(
              value.summary_target_mode === "auto" ? "auto" : "manual",
            );
          }
        }
      })
      .catch((error) => {
        if (!disposed) setError(error.message);
      });
    return () => {
      disposed = true;
    };
  }, [chat, contextRevision]);
  const allowed = contextActions(state, busy);
  async function saveDefault() {
    if (!state) return;
    setBusy(true);
    setError("");
    setResult("");
    try {
      const limit = targetMode === "manual" ? summaryByteLimit(bytes) : 0;
      await api(
        "settings/context",
        {
          ...(limit ? { task_summary_bytes: limit } : {}),
          summary_target_mode: targetMode,
          automatic_compaction: true,
          expected_revisions:
            draftRevisions.current || state.expected_revisions,
        },
        "PATCH",
      );
      const saved = await api<ContextState>(
        `attach/context?conversation_id=${encodeURIComponent(chat)}`,
      );
      setState(saved);
      editing.current = false;
      draftRevisions.current = saved.expected_revisions;
      setBytes(saved.byte_limit);
      setResult(
        targetMode === "auto"
          ? "Core 默认已保存为自动摘要目标，自动压缩已开启；已有摘要保留，新请求使用新配置。"
          : `Core 默认摘要上限已保存为 ${saved.byte_limit.toLocaleString()} 字节，自动压缩已开启；已有摘要保留，可手动重新压缩后继续受限任务。`,
      );
      onDone();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function action(action: string) {
    if (!state) return;
    if (
      action === "clear" &&
      !window.confirm("清除当前上下文摘要？原始对话和实际执行记录保留。")
    )
      return;
    let limit: number;
    try {
      limit = targetMode === "manual" ? summaryByteLimit(bytes) : 0;
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
          ...(action !== "clear"
            ? {
                summary_target_mode: targetMode,
                ...(limit ? { summary_bytes: limit } : {}),
              }
            : {}),
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
      <p>
        {busy || state?.compaction_state === "dispatched"
          ? "正在生成上下文摘要…"
          : state?.compaction_state === "unknown"
            ? "摘要请求结果未知，请先核对记录。"
            : contextStatus(state)}
      </p>
      {state && (
        <>
          <p className="form-hint">
            {contextScopeLabel(state.visibility_scope)} ·
            {state.automatic ? "自动压缩已开启" : "自动压缩已关闭"} · 触发比例{" "}
            {state.window.automatic_percent}%
          </p>
          <p className="form-hint">
            当前 Core 默认摘要上限：{state.byte_limit.toLocaleString()} UTF-8
            字节（不是 token）。
            {state.summary?.byte_limit
              ? `已有摘要生成上限：${state.summary.byte_limit.toLocaleString()} 字节。`
              : ""}
            <a
              href={`?view=settings&section=context&chat=${encodeURIComponent(chat)}`}
            >
              自动压缩与请求预算设置
            </a>
            {" · "}
            <a
              href={`?view=settings&section=providers&chat=${encodeURIComponent(chat)}`}
            >
              模型窗口与辅助思考
            </a>
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
          <p className="form-hint">
            本地请求字节保护：{state.window.byte_limit.toLocaleString()}{" "}
            字节；它与模型 token 窗口分别校验。
            {state.last_request?.limiting_resource === "bytes"
              ? "当前主要受字节保护约束。"
              : ""}
            {state.last_request?.estimate_method ===
            "matched_attempt_prefix_plus_new_input"
              ? "估算已用匹配请求的实际输入用量校准。"
              : "本地估算不等于厂商精确 token 数。"}
          </p>
          {state.last_reported_usage && (
            <p className="form-hint">
              最近模型实报输入：
              {state.last_reported_usage.input_tokens.toLocaleString()} tokens
              {state.last_reported_usage.cache_read_tokens !== undefined
                ? ` · 缓存读取 ${state.last_reported_usage.cache_read_tokens.toLocaleString()} tokens`
                : " · 缓存用量未知"}
              {state.last_request?.attempt_id !== state.last_usage_attempt_id
                ? "。该用量来自最近已发送请求，不等于当前准备请求的实测值。"
                : "。与上方请求关联。"}
            </p>
          )}
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
                    {(
                      {
                        instructions: "固定规则",
                        tools: "工具定义",
                        memory: "Memory",
                        skills: "Skill",
                        extensions: "扩展材料",
                        summary: "历史摘要",
                        unresolved: "未决事实",
                        history: "历史原文",
                        history_omitted: "省略历史的引用",
                        tool_results: "工具结果",
                        readback: "历史回读",
                        current_input: "当前输入",
                        images: "图片",
                      } as Record<string, string>
                    )[part.kind] || part.kind}
                    ：{part.bytes.toLocaleString()} 字节 / 估算{" "}
                    {part.estimated_tokens.toLocaleString()} tokens
                  </li>
                ))}
              </ul>
            </details>
          )}
          <Field
            label="本次摘要目标"
            hint={
              state.summary_target_mode === "legacy_manual"
                ? "旧配置按原值保留；选择自动后可保存为默认，不会凭数值猜测原设置意图。"
                : "本次目标与自动压缩开关分别管理。"
            }
          >
            <Choice
              label="本次摘要目标"
              value={targetMode}
              options={[
                ["auto", "自动目标"],
                ["manual", "手动字节上限"],
              ]}
              disabled={busy}
              onChange={(mode) => {
                editing.current = true;
                setTargetMode(mode);
              }}
            />
          </Field>
          {state.summary_target_tokens ? (
            <p className="form-hint">
              自动软目标约 {state.summary_target_tokens.toLocaleString()}{" "}
              tokens；保存字节保护独立校验。
            </p>
          ) : null}
          <Field
            label="摘要目标（UTF-8 字节）"
            hint="手动压缩仅使用本次目标；保存默认设置影响此 Core 的后续请求，并开启自动压缩。"
          >
            <input
              type="number"
              min={1}
              max={16384}
              step={1}
              disabled={busy || targetMode === "auto"}
              value={bytes}
              onChange={(event) => {
                editing.current = true;
                setBytes(Number(event.target.value));
              }}
            />
          </Field>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => void saveDefault()}
          >
            保存默认设置
          </Button>
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
          仅本次手动压缩
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
