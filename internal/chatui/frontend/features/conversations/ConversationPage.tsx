import {
  memo,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ArrowDown,
  ArrowUp,
  Check,
  CircleHelp,
  TriangleAlert,
  ChevronRight,
  Clock3,
  FileText,
  History,
  LoaderCircle,
  Paperclip,
  Square,
  WandSparkles,
  X,
} from "lucide-react";
import { api, APIError, headers, upload } from "@/lib/api/client";
import type {
  Attachment,
  CapabilitySelection,
  ChatSnapshot,
  Confirmation,
  Conversation,
  Permission,
  Stream,
  Turn,
  ReasoningCapabilities,
} from "@/lib/api/types";
import {
  current,
  loadNavigation,
  loadSnapshot,
  notify,
  report,
  useRuntime,
} from "@/lib/store/runtime";
import {
  attachmentDraft,
  saveAttachmentDraft,
  messageDraft,
  saveMessageDraft,
  pendingSend,
  savePendingSend,
  consumeSentDraft,
  branchRequestKey,
  clearBranchRequest,
  type PendingSend,
} from "@/lib/drafts";
import { Button } from "@/components/ui/button";
import { Choice } from "@/components/Choice";
import {
  ReasoningChoice,
  reasoningSelection,
} from "@/components/ReasoningChoice";
import { Divider } from "@/components/Divider";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
  DialogHeader,
} from "@/components/ui/dialog";
import { ContextPanel } from "./ContextPanel";
import { DeliveryPanel } from "./DeliveryPanel";
import { SourceAnalysis } from "./SourceAnalysis";
import { StickerPanel } from "./StickerPanel";
import { Capabilities, ExtensionTurnChoices } from "./Capabilities";
import { SlashCommands, type SlashEntry } from "./SlashCommands";
import {
  commandQuery,
  commandNeedsInput,
  commandInputDefaults,
} from "../../lib/presentation/commands.mjs";
import { ApprovalView } from "@/components/ApprovalView";
import { ChatMessage } from "@/features/messages/ChatMessage";
import { Message } from "@/features/messages/Message";

interface PendingInput {
  capabilities?: CapabilitySelection;
  attachments?: Attachment[];
  reasoning?: ReasoningCapabilities["default"];
  key: string;
  action: string;
  status: string;
  prompt?: string;
  target_run?: string;
}
interface Inputs {
  entries: PendingInput[];
  paused: boolean;
}
const emptyStreams: Stream[] = [];
const scrollPositions = new Map<string, number>();
const statusLabels: Record<string, string> = {
  generating: "运行中",
  awaiting_approval: "等待确认",
  completed: "已完成",
  unknown: "结果未知",
  stopped: "已停止",
  failed: "失败",
  limited: "本轮已结算",
  needs_attention: "需要处理",
};

function ApprovalCard({
  turn,
  chat,
  onDone,
}: {
  turn: Turn;
  chat: string;
  onDone: () => void;
}) {
  const [preview, setPreview] = useState<Confirmation | null>(
    turn.confirmation || null,
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [code, setCode] = useState("");
  useEffect(() => {
    let disposed = false;
    setPreview(turn.confirmation || null);
    setError("");
    setCode("");
    api<Confirmation>(
      `chats/${chat}/preview?run_id=${encodeURIComponent(turn.key)}`,
      {},
    )
      .then(async (value) => {
        if (disposed) return;
        if (value.run_id !== turn.key)
          throw new Error("审批目标已变化，请刷新当前任务。");
        setPreview(value);
        if (value.control_operation_id) {
          const confirmation = await api<{
            code: string;
            operation_id: string;
          }>(
            `control/confirmation?operation_id=${encodeURIComponent(value.control_operation_id)}&conversation_id=${encodeURIComponent(chat)}`,
          );
          if (
            !disposed &&
            confirmation.operation_id === value.control_operation_id
          )
            setCode(confirmation.code);
        }
      })
      .catch((error) => {
        if (!disposed) setError(error.message);
      });
    return () => {
      disposed = true;
    };
  }, [chat, turn.key, turn.confirmation?.digest]);
  async function decide(approved: boolean) {
    if (!preview) return;
    setBusy(true);
    setError("");
    try {
      await api(
        `chats/${chat}/confirm?run_id=${encodeURIComponent(turn.key)}`,
        {
          confirmation: preview,
          approved,
          ...(code ? { code } : {}),
        },
      );
      onDone();
    } catch (error) {
      setError(
        error instanceof Error ? error.message : "确认未完成，请刷新预览。",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <ApprovalView
      preview={preview}
      busy={busy}
      error={error}
      ready={!preview?.control_operation_id || Boolean(code)}
      onDecide={(approved) => void decide(approved)}
    />
  );
}

const TurnView = memo(function TurnView({
  turn,
  chat,
  onRefresh,
  streams,
  onRestart,
  onContext,
}: {
  turn: Turn;
  chat: string;
  onRefresh: () => void;
  streams: Stream[];
  onRestart: (message: string, mode: string) => void;
  onContext: () => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const [busy, setBusy] = useState(false);
  const state = statusLabels[turn.state] || "需要处理";
  async function action(name: string, body: unknown = {}) {
    setBusy(true);
    try {
      await api(
        `chats/${chat}/${name}?run_id=${encodeURIComponent(turn.key)}`,
        body,
      );
      onRefresh();
    } catch (error) {
      report(error);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className={`turn state-${turn.state}`} data-run={turn.key}>
      {turn.trigger && (
        <p className="form-hint">
          主动对话评估 · {turn.trigger.extension_id} · {turn.reason || state}
        </p>
      )}
      {turn.messages.map((message) => (
        <ChatMessage
          key={message.key}
          message={message}
          chat={chat}
          model={turn.model_id}
          elapsed={turn.elapsed_ms}
          run={turn.key}
          onRestart={onRestart}
        />
      ))}
      {turn.activity.length > 0 && (
        <div className="tool-group">
          <button
            className="tool-summary"
            onClick={() => setExpanded(!expanded)}
          >
            <ChevronRight size={15} className={expanded ? "expanded" : ""} />
            {turn.state === "completed" ? (
              <Check size={15} />
            ) : turn.state === "unknown" ? (
              <CircleHelp size={15} />
            ) : turn.state === "failed" ? (
              <TriangleAlert size={15} />
            ) : (
              <Clock3 size={15} />
            )}
            <span>{turn.activity.length} 项执行记录</span>
            <time>{(turn.elapsed_ms / 1000).toFixed(1)} 秒</time>
          </button>
          {expanded && (
            <div className="tool-list">
              {turn.activity.map((item) => (
                <details key={item.key}>
                  <summary>
                    <code>{item.name || item.kind}</code>
                    <span>{item.status}</span>
                    <time>
                      {item.duration_ms === undefined
                        ? ""
                        : item.duration_ms + " ms"}
                    </time>
                  </summary>
                  {item.target && <p>{item.target}</p>}
                  {item.requested_reasoning && (
                    <p>
                      请求的思考设置：
                      {item.requested_reasoning.effort ||
                        item.requested_reasoning.mode ||
                        "Provider 默认"}
                      （服务端未回显档位）
                    </p>
                  )}
                  {item.usage && (
                    <p>
                      输入 {item.usage.input_tokens} / 输出{" "}
                      {item.usage.output_tokens} token；推理{" "}
                      {item.usage.reasoning_tokens === undefined
                        ? "未知"
                        : item.usage.reasoning_tokens}
                    </p>
                  )}
                  {item.text && <pre>{item.text}</pre>}
                  {item.receipts?.map((receipt, index) => (
                    <p key={index}>
                      {receipt.kind} · {receipt.status}
                      {receipt.host_escape_hatch ? " · host" : ""}
                    </p>
                  ))}
                </details>
              ))}
            </div>
          )}
        </div>
      )}
      {Array.isArray(turn.sources) && turn.sources.length > 0 && (
        <details className="run-evidence">
          <summary>来源与上下文（{turn.sources.length}）</summary>
          <ul>
            {turn.sources.map((source, index) => (
              <li key={index}>
                {source.title || source.scope?.owner || "显式上下文"} ·{" "}
                {source.scope?.kind} · {source.state || ""}
                {source.revision ? ` · 版本 ${source.revision}` : ""}
              </li>
            ))}
          </ul>
        </details>
      )}
      {turn.state !== "generating" && turn.state !== "awaiting_approval" && (
        <SourceAnalysis chat={chat} turn={turn} />
      )}
      {streams.map((stream) => (
        <article className="assistant-message" key={stream.attempt}>
          <Message text={stream.raw} streaming={!stream.interrupted} />
          {stream.interrupted && (
            <p className="inline-warning">
              输出已中断，保留已有片段；最终状态以运行记录为准。
            </p>
          )}
        </article>
      ))}
      {turn.attachments?.map((ref: Attachment) => (
        <button
          key={ref.id}
          className="attachment-chip"
          onClick={() => downloadAttachment(chat, ref)}
        >
          <FileText size={14} />
          {ref.name}
        </button>
      ))}
      {turn.messages_has_earlier && (
        <p className="form-hint">
          本轮较早消息保留在原始记录，可通过 CLI /history 按页查看。
        </p>
      )}
      {turn.activity_has_earlier && (
        <p className="form-hint">
          当前显示最近 128 项执行记录，完整记录可用 CLI inspect 查看。
        </p>
      )}
      {turn.evidence && (
        <details className="run-evidence">
          <summary>查看执行证据</summary>
          <pre>{JSON.stringify(turn.evidence, null, 2)}</pre>
        </details>
      )}
      {turn.state === "awaiting_approval" && (
        <ApprovalCard turn={turn} chat={chat} onDone={onRefresh} />
      )}
      {!["completed", "generating", "awaiting_approval"].includes(
        turn.state,
      ) && (
        <div
          className={`state-notice ${turn.state === "unknown" ? "unknown" : turn.state === "failed" ? "failure" : ""}`}
        >
          <div>
            <strong>{state}</strong>
            {turn.reason && <p>{turn.reason}</p>}
            {turn.state === "unknown" && (
              <p>结果还不能确认。原执行记录已保留，不自动重放副作用。</p>
            )}
            <div className="run-actions">
              {turn.state === "limited" && (
                <Button variant="outline" onClick={onContext}>
                  调整上下文与压缩
                </Button>
              )}
              {turn.can_continue && (
                <Button
                  variant="outline"
                  disabled={busy}
                  onClick={() => action("continue")}
                >
                  继续
                </Button>
              )}
              {turn.can_end && (
                <Button
                  variant="ghost"
                  disabled={busy}
                  onClick={() => {
                    if (
                      window.confirm(
                        "结束当前任务并保留已有作用和 UNKNOWN 记录？",
                      )
                    )
                      void action("end", { acknowledge: true });
                  }}
                >
                  结束任务
                </Button>
              )}
              <Button variant="ghost" onClick={onRefresh}>
                刷新记录
              </Button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
});

async function downloadAttachment(chat: string, ref: Attachment) {
  try {
    const response = await fetch(
      `/api/attachments/${encodeURIComponent(ref.id)}?conversation_id=${encodeURIComponent(chat)}`,
      { headers: headers(), credentials: "same-origin" },
    );
    if (!response.ok) throw new Error("附件下载失败。");
    const url = URL.createObjectURL(await response.blob());
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = ref.name;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 30000);
  } catch (error) {
    report(error);
  }
}

export default function ConversationPage({
  reading,
  onReading,
  composerMax,
  onComposerMax,
  onOpenChat,
  onOpenModelSettings,
  onViewCommand,
}: {
  reading: number;
  onReading: (width: number) => void;
  composerMax: number;
  onComposerMax: (height: number) => void;
  onOpenChat: (id: string) => Promise<void>;
  onOpenModelSettings?: () => void;
  onViewCommand?: (text: string) => boolean;
}) {
  const state = useRuntime();
  const [draft, setDraft] = useState(messageDraft(state.selected));
  const earlyDraft = useRef<{ chat: string; text: string } | null>(null);
  const [model, setModel] = useState("");
  const [modelDirty, setModelDirty] = useState(false);
  const [reasoning, setReasoning] = useState("inherit");
  const [turnSettings, setTurnSettings] =
    useState<CapabilitySelection["extension_settings"]>();
  useEffect(() => setReasoning("inherit"), [model, state.selected]);
  const [permission, setPermission] = useState<Permission>("none");
  const [fullConfirmed, setFullConfirmed] = useState(false);
  const [mutating, setMutating] = useState(false);
  const [attachments, setAttachments] = useState<Attachment[]>(() =>
    attachmentDraft(state.selected),
  );
  const [inputs, setInputs] = useState<Inputs>({ entries: [], paused: false });
  const [earlier, setEarlier] = useState<Turn[]>([]);
  const [following, setFollowing] = useState(true);
  const reader = useRef<HTMLDivElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const restoredScroll = useRef("");
  const [contextOpen, setContextOpen] = useState(false);
  const [capabilities, setCapabilities] = useState<CapabilitySelection>({});
  const [capabilitySaving, setCapabilitySaving] = useState(false);
  const capabilityRequest = useRef(false);
  useEffect(() => {
    const catalog = state.environment?.capabilities;
    if (!catalog) return;
    const enabled = new Set(
      catalog.extensions.filter((item) => item.enabled).map((item) => item.id),
    );
    setCapabilities((previous) => {
      const extensions = previous.extensions?.filter((id) => enabled.has(id));
      if (extensions?.length === previous.extensions?.length) return previous;
      return {
        ...previous,
        extensions,
        extension_variants: Object.fromEntries(
          Object.entries(previous.extension_variants || {}).filter(([id]) =>
            enabled.has(id),
          ),
        ),
        extension_settings: Object.fromEntries(
          Object.entries(previous.extension_settings || {}).filter(([id]) =>
            enabled.has(id),
          ),
        ),
      };
    });
    setTurnSettings((previous) =>
      previous === undefined
        ? undefined
        : Object.fromEntries(
            Object.entries(previous).filter(([id]) => enabled.has(id)),
          ),
    );
  }, [state.environment?.capabilities]);
  const pendingPermission = useRef<{
    mode: Permission;
    confirmed: boolean;
  } | null>(null);
  const [earlierCursor, setEarlierCursor] = useState("");
  const [hasEarlier, setHasEarlier] = useState<boolean | null>(null);
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  const latest = state.snapshot?.view.turns.at(-1);
  const runBusy = Boolean(
    state.snapshot?.busy || latest?.state === "generating",
  );
  useEffect(() => {
    setEarlier([]);
    setEarlierCursor("");
    setHasEarlier(null);
    setInputs({ entries: [], paused: false });
    setContextOpen(false);
    setCapabilities({});
    setTurnSettings(undefined);
    setFullConfirmed(false);
    pendingPermission.current = null;
    setModel(state.snapshot?.view.model_id || "");
    setModelDirty(false);
  }, [state.selected]);
  useEffect(() => {
    // On reload the URL already selects a chat before its scoped drafts have
    // loaded. Restore once storage is ready, while preserving startup typing.
    if (state.environment && earlyDraft.current) {
      saveMessageDraft(earlyDraft.current.chat, earlyDraft.current.text);
      earlyDraft.current = null;
    }
    setDraft(messageDraft(state.selected));
    setAttachments(attachmentDraft(state.selected));
  }, [state.selected, state.environment?.environment]);
  useEffect(() => {
    if (state.snapshot) {
      setPermission(state.snapshot.view.permission.mode);
      if (!modelDirty) setModel(state.snapshot.view.model_id);
    }
  }, [
    state.snapshot?.view.permission.mode,
    state.snapshot?.view.model_id,
    modelDirty,
  ]);
  useEffect(() => {
    if (state.snapshot && !capabilityRequest.current)
      setCapabilities(state.snapshot.view.capabilities || {});
  }, [state.snapshot?.view.capabilities]);
  useEffect(() => {
    if (!state.selected) return;
    let disposed = false;
    api<Inputs>(
      `attach/inputs?conversation_id=${encodeURIComponent(state.selected)}`,
    )
      .then((value) => {
        if (!disposed) setInputs(value);
      })
      .catch(() => {});
    return () => {
      disposed = true;
    };
  }, [state.selected, state.inputRevision]);
  useLayoutEffect(() => {
    if (textarea.current) {
      textarea.current.style.height = "auto";
      textarea.current.style.height =
        Math.min(textarea.current.scrollHeight, composerMax) + "px";
    }
  }, [draft, composerMax]);
  useLayoutEffect(() => {
    const element = reader.current;
    if (!element || !state.snapshot) return;
    if (restoredScroll.current !== state.selected) {
      restoredScroll.current = state.selected;
      const saved = scrollPositions.get(state.selected);
      element.scrollTop = saved ?? element.scrollHeight;
      setFollowing(
        saved === undefined ||
          element.scrollHeight - element.scrollTop - element.clientHeight < 90,
      );
    } else if (following) element.scrollTop = element.scrollHeight;
  }, [state.selected, state.snapshot, following]);
  function changeDraft(value: string) {
    if (!state.environment)
      earlyDraft.current = { chat: state.selected, text: value };
    setDraft(value);
    saveMessageDraft(state.selected, value);
  }
  async function ensureChat(prompt: string): Promise<string> {
    if (!state.ready || (state.selected && !state.snapshot))
      throw new Error("正在读取目标会话和权限，请稍后发送；输入已保留。");
    if (state.selected) return state.selected;
    const selection = pendingPermission.current;
    const chat = await api<Conversation>("chats", {
      title:
        Array.from(prompt.trim().split("\n")[0]).slice(0, 80).join("") ||
        "附件对话",
      space_id: state.navSpace || "local",
    });
    saveMessageDraft(chat.id, messageDraft(""));
    if (selection)
      await api(
        `conversations/${chat.id}/permission`,
        { mode: selection.mode, confirmed: selection.confirmed },
        "PATCH",
      );
    await loadNavigation();
    await onOpenChat(chat.id);
    return chat.id;
  }
  async function choosePermission(value: string) {
    const mode = value as Permission;
    const confirmed =
      mode !== "full" ||
      window.confirm(
        "启用 Full access？已启用工具将以当前用户权限执行。权限属于当前空间，影响空间内所有对话。",
      );
    if (!confirmed) return;
    if (state.selected) {
      try {
        await api(
          `conversations/${state.selected}/permission`,
          { mode, confirmed: mode === "full" },
          "PATCH",
        );
        await loadSnapshot();
        setPermission(mode);
        setFullConfirmed(mode === "full");
      } catch (error) {
        report(error);
      }
    } else {
      pendingPermission.current = { mode, confirmed: mode === "full" };
      setPermission(mode);
      setFullConfirmed(mode === "full");
    }
  }
  const refresh = useCallback(async () => {
    await Promise.all([loadSnapshot(), loadNavigation()]);
  }, []);
  const onRefresh = useCallback(() => {
    void refresh().catch(report);
  }, [refresh]);
  const byRun = useMemo(() => {
    const grouped = new Map<string, Stream[]>();
    for (const stream of state.snapshot?.streams || []) {
      const items = grouped.get(stream.run) || [];
      items.push(stream);
      grouped.set(stream.run, items);
    }
    return grouped;
  }, [state.snapshot?.streams]);
  async function saveCapabilities(next: CapabilitySelection) {
    if (capabilityRequest.current) return false;
    const chat = state.selected;
    if (!chat) {
      setCapabilities(next);
      return true;
    }
    capabilityRequest.current = true;
    setCapabilitySaving(true);
    setTurnSettings(undefined);
    try {
      const selected = await api<CapabilitySelection>(
        `chats/${chat}/capabilities`,
        next,
      );
      if (current().selected === chat) setCapabilities(selected);
      await loadSnapshot(chat);
      return true;
    } catch (error) {
      report(error);
      return false;
    } finally {
      capabilityRequest.current = false;
      setCapabilitySaving(false);
    }
  }
  async function chooseCommand(entry: SlashEntry) {
    if (entry.kind === "builtin") {
      setContextOpen(true);
      changeDraft("");
      return;
    }
    if (entry.kind === "skill") {
      const next = {
        ...capabilities,
        skills: [...new Set([...(capabilities.skills || []), entry.name])],
      };
      if (!(await saveCapabilities(next))) return;
      changeDraft(`使用 Skill ${entry.name}：`);
      textarea.current?.focus();
      return;
    }
    if (runBusy) {
      report(new Error("当前任务运行中，请先结束后显式启动工作流。"));
      return;
    }
    let input: unknown = {};
    if (commandNeedsInput(entry)) {
      const raw = window.prompt(
        `填写 /${entry.name} 的输入 JSON，字段：${entry.inputSummary || "见扩展说明"}`,
        commandInputDefaults(entry),
      );
      if (raw === null) return;
      try {
        input = JSON.parse(raw);
      } catch {
        report(new Error("工作流输入必须是有效 JSON。"));
        return;
      }
    }
    setMutating(true);
    try {
      const chat = await ensureChat(`/${entry.name}`);
      await api(`chats/${chat}/workflow`, {
        extension: entry.extension,
        workflow: entry.name,
        input,
        request_key: crypto.randomUUID(),
      });
      changeDraft("");
      await refresh();
    } catch (error) {
      report(error);
    } finally {
      setMutating(false);
    }
  }
  async function send() {
    const prompt = draft.trim();
    if (
      !prompt ||
      mutating ||
      !state.ready ||
      (state.selected && !state.snapshot)
    )
      return;
    try {
      if (onViewCommand?.(prompt)) {
        changeDraft("");
        return;
      }
    } catch (error) {
      report(error);
      return;
    }
    if (prompt === "/compact" || prompt === "/context") {
      setContextOpen(true);
      changeDraft("");
      return;
    }
    if (prompt === "/stop") {
      await api(`chats/${state.selected}/stop`, {}).then(refresh).catch(report);
      return;
    }
    if (prompt === "/continue") {
      await api(`chats/${state.selected}/continue`, {})
        .then(refresh)
        .catch(report);
      return;
    }
    setMutating(true);
    const previousChat = state.selected;
    let chat = previousChat;
    try {
      chat = await ensureChat(prompt);
      const existing = pendingSend(chat);
      if (existing) {
        let found = false;
        if (existing.kind === "submit")
          found = (
            await api<{ found: boolean }>(
              `conversations/${encodeURIComponent(chat)}/requests/${encodeURIComponent(existing.request_key)}/status`,
            )
          ).found;
        else
          found = (
            await api<Inputs>(
              `attach/inputs?conversation_id=${encodeURIComponent(chat)}`,
            )
          ).entries.some((item) => item.key === existing.request_key);
        if (found) {
          consumeSentDraft(chat, existing);
          if (current().selected === chat) {
            setDraft(messageDraft(chat));
            setAttachments(attachmentDraft(chat));
          }
          await refresh();
          notify("已核对上一条消息，原请求已经接收");
          return;
        }
        if (existing.prompt !== prompt)
          throw new Error(
            "上一条发送结果尚未核对，请恢复原文后用同一个请求键重试。",
          );
      }
      const command: PendingSend = existing || {
        request_key: crypto.randomUUID(),
        prompt,
        attachments: attachments.map((ref) => ref.id),
        kind: runBusy ? "queue" : "submit",
        permission,
        confirm_full_access: fullConfirmed,
        capabilities: {
          ...capabilities,
          ...(turnSettings === undefined
            ? {}
            : { extension_settings: turnSettings }),
        },
        model: modelDirty ? model : "",
        reasoning: reasoningSelection(reasoning),
      };
      savePendingSend(chat, command);
      await submitCommand(chat, command);
      if (
        previousChat !== chat &&
        messageDraft(previousChat).trim() === command.prompt
      )
        saveMessageDraft(previousChat, "");
      await refresh();
    } catch (error) {
      if (
        error instanceof APIError &&
        error.status >= 400 &&
        error.status < 500 &&
        error.code !== "attention"
      )
        savePendingSend(chat, null);
      report(error);
    } finally {
      setMutating(false);
    }
  }
  async function submitCommand(chat: string, command: PendingSend) {
    savePendingSend(chat, command);
    const { kind, model: chosenModel, ...body } = command;
    if (kind === "queue") {
      if (
        chosenModel === "auto" ||
        (!chosenModel && current().snapshot?.view.model_id === "auto")
      )
        throw new APIError(
          "排队消息需要选择一个具体模型，请先切换模型后再发送。",
          400,
          "queue_model",
        );
      await api("attach/inputs", {
        conversation_id: chat,
        operation: "accept",
        command: {
          request_key: command.request_key,
          prompt: command.prompt,
          attachments: command.attachments,
          action: "queue",
          reasoning: command.reasoning,
          capabilities: command.capabilities,
          model_id: chosenModel,
        },
      });
    } else {
      if (chosenModel) await api(`chats/${chat}/model`, { model: chosenModel });
      await api(`chats/${chat}/submit`, body);
    }
    consumeSentDraft(chat, command);
    if (current().selected === chat) {
      setDraft(messageDraft(chat));
      setAttachments(attachmentDraft(chat));
      setModelDirty(false);
      if (!messageDraft(chat)) {
        setReasoning("inherit");
        setTurnSettings(undefined);
      }
    }
  }
  async function addFiles(files: FileList | null) {
    if (!files || mutating) return;
    const selectedFiles = Array.from(files);
    const policy = state.environment?.attachments as
      | {
          max_files?: number;
          max_batch_bytes?: number;
          max_input_bytes?: number;
        }
      | undefined;
    const existingFiles = attachmentDraft(state.selected);
    if (
      existingFiles.length + selectedFiles.length >
      (policy?.max_files || 8)
    ) {
      report(
        new Error(
          `每条消息最多选择 ${policy?.max_files || 8} 个附件，请减少文件后重试。`,
        ),
      );
      return;
    }
    const bytes =
      existingFiles.reduce((sum, file) => sum + file.bytes, 0) +
      selectedFiles.reduce((sum, file) => sum + file.size, 0);
    if (
      (policy?.max_batch_bytes && bytes > policy.max_batch_bytes) ||
      selectedFiles.some(
        (file) => policy?.max_input_bytes && file.size > policy.max_input_bytes,
      )
    ) {
      report(
        new Error(
          "所选附件超过单文件或总大小限制，请减少附件；当前输入已保留。",
        ),
      );
      return;
    }
    setMutating(true);
    try {
      const chat = await ensureChat(draft || "附件对话");
      for (const file of selectedFiles) {
        const ref = (await upload(chat, file)) as Attachment;
        const values = [...attachmentDraft(chat), ref];
        saveAttachmentDraft(chat, values);
        if (current().selected === chat) setAttachments(values);
      }
    } catch (error) {
      report(error);
    } finally {
      setMutating(false);
    }
  }
  async function inputAction(operation: string, key?: string) {
    const chat = state.selected;
    if (!chat) return;
    const input = inputs.entries.find((item) => item.key === key);
    if (
      operation === "steer" &&
      (input?.reasoning || input?.capabilities) &&
      !window.confirm(
        "转为引导只发送文字和附件，放弃这条排队消息的思考和能力选择；当前任务的模型和配置保持不变。继续？",
      )
    )
      return;
    try {
      await api("attach/inputs", {
        conversation_id: chat,
        operation,
        key,
        run_id: latest?.key || "",
        discard_options: operation === "steer",
      });
      const next = await api<Inputs>(`attach/inputs?conversation_id=${chat}`);
      if (current().selected === chat) setInputs(next);
    } catch (error) {
      report(error);
    }
  }
  async function restoreInput(input: PendingInput) {
    const chat = state.selected;
    if (
      !chat ||
      !window.confirm(
        "把这条待发消息退回输入框，并按当前配置重新提交？已有文字和附件会保留。",
      )
    )
      return;
    try {
      const result = await api<PendingInput>("attach/inputs", {
        conversation_id: chat,
        operation: "cancel",
        key: input.key,
      });
      if (result.status !== "cancelled")
        throw new Error("这条消息已被任务接收，请查看记录，不能重复发送。");
      const text = [messageDraft(chat), input.prompt || ""]
        .filter(Boolean)
        .join("\n\n");
      saveMessageDraft(chat, text);
      saveAttachmentDraft(chat, [
        ...new Map(
          [...attachmentDraft(chat), ...(input.attachments || [])].map(
            (ref) => [ref.id, ref],
          ),
        ).values(),
      ]);
      const next = await api<Inputs>(`attach/inputs?conversation_id=${chat}`);
      if (current().selected === chat) {
        setInputs(next);
        setDraft(text);
        setAttachments(attachmentDraft(chat));
        setReasoning("inherit");
      }
      notify("消息已退回输入框，发送前可核对当前模型与权限。");
    } catch (error) {
      report(error);
    }
  }
  async function loadEarlier() {
    if (!state.selected || !state.snapshot || loadingEarlier) return;
    const chat = state.selected,
      before = earlierCursor || state.snapshot.view.before || "";
    if (!before) return;
    setLoadingEarlier(true);
    const oldHeight = reader.current?.scrollHeight || 0;
    try {
      const snapshot = await api<ChatSnapshot>(
        `chats/${encodeURIComponent(chat)}?before=${encodeURIComponent(before)}`,
      );
      if (current().selected !== chat) return;
      setEarlier((values) => [
        ...snapshot.view.turns.filter(
          (turn) => !values.some((old) => old.key === turn.key),
        ),
        ...values,
      ]);
      setEarlierCursor(snapshot.view.before || "");
      setHasEarlier(Boolean(snapshot.view.has_earlier));
      requestAnimationFrame(() => {
        if (reader.current)
          reader.current.scrollTop += reader.current.scrollHeight - oldHeight;
      });
    } catch (error) {
      report(error);
    } finally {
      setLoadingEarlier(false);
    }
  }
  const restart = useCallback(
    async (messageID: string, mode: string) => {
      const snapshot = current().snapshot;
      if (!snapshot) return;
      const original = [...earlier, ...snapshot.view.turns]
        .flatMap((turn) => turn.messages)
        .find((message) => message.key === messageID);
      let raw = original?.raw || "";
      if (original?.truncated) {
        try {
          raw = (
            await api<{ raw: string }>(
              `chats/${snapshot.view.conversation.id}/raw?kind=message&key=${encodeURIComponent(messageID)}`,
            )
          ).raw;
        } catch (error) {
          report(error);
          return;
        }
      }
      const prompt = window.prompt(
        mode === "rerun"
          ? "编辑后在新分支执行；工作区文件不会回滚。"
          : "从这条回复创建分支，输入下一条要求。",
        mode === "rerun" ? raw : "",
      );
      if (prompt === null) return;
      setMutating(true);
      let branchID = "";
      try {
        const sourceChat = snapshot.view.conversation.id;
        const branchKey = branchRequestKey(
          sourceChat,
          JSON.stringify([messageID, mode, prompt]),
        );
        const result = await api<{
          conversation: Conversation;
          prompt?: string;
          attachments?: Attachment[];
        }>("attach/branch", {
          conversation_id: snapshot.view.conversation.id,
          command: {
            request_key: branchKey,
            message_id: Number(messageID),
            mode,
            prompt,
          },
        });
        branchID = result.conversation.id;
        clearBranchRequest(sourceChat);
        await loadNavigation();
        await onOpenChat(result.conversation.id);
        saveMessageDraft(result.conversation.id, result.prompt || "");
        setDraft(result.prompt || "");
        saveAttachmentDraft(result.conversation.id, result.attachments || []);
        setAttachments(result.attachments || []);
        if (result.prompt?.trim()) {
          const selected = current().snapshot?.view;
          const command: PendingSend = {
            request_key: crypto.randomUUID(),
            prompt: result.prompt,
            attachments: (result.attachments || []).map((ref) => ref.id),
            kind: "submit",
            model: "",
            permission: selected?.permission.mode || "none",
            confirm_full_access: false,
            capabilities: selected?.capabilities || {},
          };
          await submitCommand(result.conversation.id, command);
          await loadSnapshot(result.conversation.id);
        }
      } catch (error) {
        if (
          error instanceof APIError &&
          error.status >= 400 &&
          error.status < 500 &&
          error.code !== "attention"
        ) {
          if (branchID) savePendingSend(branchID, null);
          else clearBranchRequest(snapshot.view.conversation.id);
        }
        report(error);
      } finally {
        setMutating(false);
      }
    },
    [onOpenChat, earlier],
  );
  const modelOptions = Object.entries(state.environment?.models || {})
    .filter(([, model]) => model.provider_enabled !== false && !model.missing)
    .map(([id, model]) => [id, model.model || id] as [string, string]);
  if (state.environment?.auto === true)
    modelOptions.unshift(["auto", "自动路由"]);
  return (
    <>
      <section
        className="messages"
        ref={reader}
        aria-label="对话消息"
        onScroll={() => {
          if (reader.current) {
            scrollPositions.set(state.selected, reader.current.scrollTop);
            setFollowing(
              reader.current.scrollHeight -
                reader.current.scrollTop -
                reader.current.clientHeight <
                90,
            );
          }
        }}
      >
        <div className="reading-frame">
          <div className="reading-column">
            {state.snapshot?.view.conversation.space_id !== "local" &&
              state.selected && (
                <>
                  <DeliveryPanel
                    chat={state.selected}
                    run={latest?.key || ""}
                    revision={(latest?.key || "") + latest?.state}
                  />
                  {state.spaces.find(
                    (space) =>
                      space.id === state.snapshot?.view.conversation.space_id,
                  )?.source === "telegram" && (
                    <StickerPanel key={state.selected} chat={state.selected} />
                  )}
                </>
              )}
            {state.snapshot?.view.background?.length ? (
              <details className="run-evidence">
                <summary>收到的背景与渠道命令（未自动执行）</summary>
                {state.snapshot.view.background.map((message) => (
                  <p key={message.key}>{message.raw}</p>
                ))}
              </details>
            ) : null}
            {(hasEarlier ?? state.snapshot?.view.has_earlier) && (
              <button
                className="load-earlier"
                disabled={loadingEarlier}
                onClick={loadEarlier}
              >
                {loadingEarlier ? "正在读取…" : "加载更早的消息"}
              </button>
            )}
            {!state.selected ? (
              <div className="empty-conversation">
                <MessageSquareMark />
                <h1>从一件具体的事开始</h1>
                <p>
                  {modelOptions.length
                    ? "描述目标、附上文件，使用你选择的能力继续工作。"
                    : "先在设置中配置并开启一个 Provider，再开始对话。"}
                </p>
              </div>
            ) : state.loading && !state.snapshot ? (
              <p className="loading-label">正在读取对话…</p>
            ) : null}
            {[
              ...new Map(
                [...earlier, ...(state.snapshot?.view.turns || [])].map(
                  (turn) => [turn.key, turn],
                ),
              ).values(),
            ].map((turn) => (
              <TurnView
                key={turn.key}
                turn={turn}
                chat={state.selected}
                streams={byRun.get(turn.key) || emptyStreams}
                onRefresh={onRefresh}
                onRestart={restart}
                onContext={() => setContextOpen(true)}
              />
            ))}
          </div>
          <Divider
            axis="x"
            label="调整阅读宽度"
            value={reading}
            min={540}
            max={1120}
            reset={820}
            onChange={onReading}
          />
        </div>
      </section>
      <section className="composer-area">
        <SlashCommands
          query={commandQuery(draft)}
          disabled={mutating}
          onChoose={(entry) => void chooseCommand(entry)}
        />
        <div className="composer-column">
          {!following && (
            <button
              className="latest-button"
              onClick={() => setFollowing(true)}
            >
              <ArrowDown size={14} />
              回到最新消息
            </button>
          )}
          {inputs.entries.filter(
            (item) =>
              item.status === "pending" || item.status === "target_ended",
          ).length > 0 && (
            <div className="queue-list">
              {inputs.entries
                .filter(
                  (item) =>
                    item.status === "pending" || item.status === "target_ended",
                )
                .map((item) => (
                  <div key={item.key}>
                    <Clock3 size={14} />
                    <span>{item.prompt || item.action}</span>
                    {item.status === "target_ended" && (
                      <span>目标任务已结束，尚未执行</span>
                    )}
                    {item.action === "queue" && latest && (
                      <button onClick={() => inputAction("steer", item.key)}>
                        转为引导
                      </button>
                    )}
                    <button onClick={() => void restoreInput(item)}>
                      退回输入框
                    </button>
                    <button
                      aria-label="删除排队消息"
                      onClick={() => inputAction("cancel", item.key)}
                    >
                      <X size={14} />
                    </button>
                  </div>
                ))}
              <button
                onClick={() => inputAction(inputs.paused ? "resume" : "pause")}
              >
                {inputs.paused ? "恢复队列" : "暂停队列"}
              </button>
            </div>
          )}
          <div
            className="composer"
            onDragOver={(event) => {
              if (event.dataTransfer.types.includes("Files"))
                event.preventDefault();
            }}
            onDrop={(event) => {
              if (event.dataTransfer.files.length) {
                event.preventDefault();
                void addFiles(event.dataTransfer.files);
              }
            }}
          >
            <Divider
              axis="y"
              label="调整输入区最大高度"
              value={composerMax}
              min={88}
              max={380}
              reset={190}
              onChange={onComposerMax}
            />
            {attachments.length > 0 && (
              <div className="attachment-drafts">
                {attachments.map((ref) => (
                  <span key={ref.id}>
                    <FileText size={14} />
                    {ref.name}
                    <button
                      aria-label={`移除 ${ref.name}`}
                      onClick={() => {
                        const values = attachments.filter(
                          (item) => item.id !== ref.id,
                        );
                        saveAttachmentDraft(state.selected, values);
                        setAttachments(values);
                      }}
                    >
                      <X size={13} />
                    </button>
                  </span>
                ))}
              </div>
            )}
            <textarea
              aria-label="消息"
              ref={textarea}
              rows={2}
              value={draft}
              placeholder={
                runBusy
                  ? "补充消息，先加入队列，也可转为引导…"
                  : "说说接下来要做什么…"
              }
              onChange={(event) => changeDraft(event.target.value)}
              onKeyDown={(event) => {
                if (
                  event.key === "Enter" &&
                  !event.shiftKey &&
                  !event.nativeEvent.isComposing &&
                  event.keyCode !== 229
                ) {
                  event.preventDefault();
                  void send();
                }
              }}
            />
            <div className="composer-tools">
              <input
                hidden
                type="file"
                multiple
                ref={fileInput}
                onChange={(event) => {
                  void addFiles(event.target.files);
                  event.target.value = "";
                }}
              />
              <button
                aria-label="添加附件"
                disabled={mutating}
                onClick={() => fileInput.current?.click()}
              >
                <Paperclip size={18} />
              </button>
              {modelOptions.length > 0 && (
                <Choice
                  label="当前模型"
                  prefix="模型："
                  value={
                    model ||
                    state.snapshot?.view.model_id ||
                    state.environment?.default_model ||
                    modelOptions[0][0]
                  }
                  options={modelOptions}
                  onChange={(value) => {
                    setModel(value);
                    setModelDirty(true);
                  }}
                />
              )}
              <Choice
                label="当前权限"
                value={permission}
                onChange={(value) => void choosePermission(value)}
                options={[
                  ["none", "仅聊天"],
                  ["ask", "每次询问"],
                  ["workspace", "工作区"],
                  ["full", "Full access"],
                ]}
              />
              <ReasoningChoice
                info={
                  state.environment?.models[
                    model ||
                      state.snapshot?.view.model_id ||
                      state.environment.default_model
                  ]?.reasoning_info
                }
                caps={
                  state.environment?.models[
                    model ||
                      state.snapshot?.view.model_id ||
                      state.environment.default_model
                  ]?.capabilities?.reasoning as
                    | ReasoningCapabilities
                    | undefined
                }
                protocol={String(
                  state.environment?.models[
                    model ||
                      state.snapshot?.view.model_id ||
                      state.environment.default_model
                  ]?.capabilities?.protocol || "chat",
                )}
                value={reasoning}
                onChange={setReasoning}
                onConfigure={onOpenModelSettings}
              />
              <ExtensionTurnChoices
                catalog={state.environment?.capabilities}
                selection={{
                  ...capabilities,
                  ...(turnSettings === undefined
                    ? {}
                    : { extension_settings: turnSettings }),
                }}
                onChange={(next) =>
                  setTurnSettings(next.extension_settings || {})
                }
              />
              <Capabilities
                conversationID={state.selected}
                catalog={state.environment?.capabilities}
                selection={capabilities}
                busy={runBusy}
                saving={capabilitySaving}
                onChange={(next) => {
                  void saveCapabilities(next);
                }}
              />
              <span className="composer-spacer" />
              {runBusy ? (
                <>
                  <button
                    aria-label="加入队列"
                    className="send-button"
                    disabled={
                      !draft.trim() ||
                      mutating ||
                      !state.ready ||
                      (Boolean(state.selected) && !state.snapshot)
                    }
                    onClick={send}
                  >
                    <ArrowUp size={18} />
                  </button>
                  <button
                    aria-label="停止运行"
                    className="send-button stop-button"
                    onClick={() =>
                      api(`chats/${state.selected}/stop`, {})
                        .then(refresh)
                        .catch(report)
                    }
                  >
                    <Square size={12} fill="currentColor" />
                  </button>
                </>
              ) : (
                <button
                  aria-label="发送消息"
                  className="send-button"
                  disabled={
                    !draft.trim() ||
                    mutating ||
                    !modelOptions.length ||
                    !state.ready ||
                    (Boolean(state.selected) && !state.snapshot)
                  }
                  onClick={send}
                >
                  {mutating ? (
                    <LoaderCircle size={17} className="spin" />
                  ) : (
                    <ArrowUp size={20} />
                  )}
                </button>
              )}
            </div>
          </div>
          <div className="composer-meta">
            {attachments.length > 0 && !draft.trim() && (
              <span>请补充你希望如何处理附件。</span>
            )}
            {(!state.ready || (state.selected && !state.snapshot)) && (
              <span>正在读取目标会话；输入会保留。</span>
            )}
            <span>
              <button
                disabled={!state.selected}
                onClick={() => setContextOpen(true)}
              >
                <History size={12} />
                上下文
              </button>
            </span>
            <span>
              {permission === "full"
                ? "Full access · 当前空间授权"
                : "Enter 发送 · Shift Enter 换行"}
            </span>
          </div>
        </div>
      </section>
      <Dialog open={contextOpen} onOpenChange={setContextOpen}>
        <DialogContent className="context-dialog">
          <DialogHeader>
            <DialogTitle>上下文管理</DialogTitle>
            <DialogDescription>
              查看实际请求预算，管理当前对话摘要。
            </DialogDescription>
          </DialogHeader>
          {contextOpen && state.selected && (
            <ContextPanel chat={state.selected} onDone={onRefresh} />
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
function MessageSquareMark() {
  return (
    <div className="empty-mark">
      <WandSparkles size={28} />
    </div>
  );
}
