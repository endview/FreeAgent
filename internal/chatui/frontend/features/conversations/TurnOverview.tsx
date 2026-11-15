import { useEffect, useMemo, useRef, useState } from "react";
import {
  Blocks,
  BookOpen,
  ChevronRight,
  Folder,
  ListTree,
  Plug,
  SlidersHorizontal,
  Wrench,
  X,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { ContextState, Turn } from "@/lib/api/types";
import { api } from "@/lib/api/client";
import { useRuntime } from "@/lib/store/runtime";
import {
  activityLabels,
  activityState,
  activityTitle,
  attentionState,
  recentActivity,
  trackActivityUpdates,
} from "@/lib/presentation/activity.mjs";
import { ActivityIcon } from "./ExecutionChain";

const permissionLabels = {
  none: "仅聊天",
  ask: "每次询问",
  workspace: "工作区",
  full: "Full access",
};
export function TurnOverview({
  chat,
  turn,
  visible,
  mobile,
  onClose,
  onMobileClose,
  onChain,
  onContext,
  onSettings,
  onGoToTurn,
}: {
  chat: string;
  turn?: Turn;
  visible: boolean;
  mobile: boolean;
  onClose: () => void;
  onMobileClose: () => void;
  onChain: (key?: string) => void;
  onContext: () => void;
  onSettings: (section: string) => void;
  onGoToTurn: () => void;
}) {
  const runtime = useRuntime();
  const [context, setContext] = useState<ContextState | null>(null),
    [contextError, setContextError] = useState("");
  useEffect(() => {
    setContext(null);
    setContextError("");
  }, [chat]);
  useEffect(() => {
    if (!chat) return;
    const controller = new AbortController();
    api<ContextState>(
      `attach/context?conversation_id=${encodeURIComponent(chat)}`,
      undefined,
      undefined,
      controller.signal,
    )
      .then((value) => {
        if (!controller.signal.aborted) {
          setContext(value);
          setContextError("");
        }
      })
      .catch((e) => {
        if (!controller.signal.aborted) setContextError(e.message);
      });
    return () => controller.abort();
  }, [chat, runtime.contextRevision]);
  const tracked = useRef<{
    run: string;
    updates: ReturnType<typeof trackActivityUpdates>;
  }>({ run: "", updates: trackActivityUpdates([]) });
  const activities = useMemo(
    () => [
      ...new Map(
        [...(turn?.attention_activity || []), ...(turn?.activity || [])].map(
          (item) => [item.key, item],
        ),
      ).values(),
    ],
    [turn?.attention_activity, turn?.activity],
  );
  const recent = useMemo(() => {
    const updates = trackActivityUpdates(
      activities,
      tracked.current.run === turn?.key ? tracked.current.updates : undefined,
    );
    tracked.current = { run: turn?.key || "", updates };
    return recentActivity(activities, turn?.state || "", updates);
  }, [turn?.key, activities, turn?.state]);
  const pending =
    turn?.attention_count ??
    (turn?.activity || []).filter((item) =>
      attentionState(activityState(item, turn?.state)),
    ).length;
  const selection = turn?.capabilities;
  const groups = [
    {
      id: "tools",
      name: "内置工具",
      Icon: Wrench,
      count: turn?.enabled_tools?.length,
    },
    {
      id: "skills",
      name: "Skill",
      Icon: BookOpen,
      count: selection?.skills?.length,
    },
    {
      id: "mcp",
      name: "MCP",
      Icon: Plug,
      count: selection?.mcp_servers?.length,
    },
    {
      id: "extensions",
      name: "扩展",
      Icon: Blocks,
      count: selection?.extensions?.length,
    },
  ];
  const modelAttempt = turn?.activity
    .filter((item) => item.kind === "model")
    .at(-1);
  const actualModels = [
    ...new Set(
      [
        ...(turn?.activity
          .filter((item) => item.kind === "model")
          .map((item) => item.model_name || item.name) || []),
        ...(turn?.messages
          .filter((item) => item.role === "assistant")
          .map((item) => item.model_name || item.model_id) || []),
      ].filter(Boolean),
    ),
  ];
  const reasoning = modelAttempt?.requested_reasoning || turn?.reasoning;
  const reasoningLabel =
    reasoning?.effort ||
    {
      enabled: "开启",
      disabled: "关闭",
      on: "开启",
      off: "关闭",
      auto: "自动",
      inherit: "Provider 默认",
    }[reasoning?.mode || ""] ||
    "Provider 默认";
  const occupancy = context?.last_request?.occupancy_percent;
  const content = (
    <>
      <div className="overview-heading">
        <strong>本轮概览</strong>
        <button
          aria-label="收起本轮概览"
          onClick={mobile ? onMobileClose : onClose}
        >
          <X size={16} />
        </button>
      </div>
      {turn ? (
        <>
          <section className="overview-section">
            <div className="overview-label">
              <strong>执行动态</strong>
              <span>
                {pending
                  ? `待处理 ${pending}${turn.attention_count === undefined && turn.activity_has_earlier ? "+" : ""}`
                  : "最近动态"}
              </span>
            </div>
            <ol className="recent-activities" aria-label="最近执行动态">
              {recent.map((item) => {
                const status = activityState(item, turn.state);
                return (
                  <li
                    key={item.key}
                    className={`activity-${status}`}
                    data-activity={item.key}
                  >
                    <button
                      onClick={() => onChain(item.key)}
                      title={`${activityTitle(item)} · ${activityLabels[status]}`}
                    >
                      <ActivityIcon status={status} />
                      <span className="activity-title">
                        {activityTitle(item)}
                      </span>
                      <span className="activity-status">
                        {activityLabels[status]}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ol>
            {!recent.length && <p>本轮尚无工具或模型执行记录。</p>}
            <button className="expand-chain" onClick={() => onChain()}>
              <ListTree size={15} />
              <span>展开完整链条</span>
              <ChevronRight size={13} />
            </button>
            {turn.state === "awaiting_approval" && (
              <button className="overview-action" onClick={onGoToTurn}>
                前往确认卡片
                <ChevronRight size={13} />
              </button>
            )}
          </section>
          <section className="overview-section">
            <div className="overview-label">
              本轮配置
              <SlidersHorizontal size={14} />
            </div>
            <dl className="run-facts">
              <div>
                <dt>模型</dt>
                <dd data-turn-model>
                  {actualModels.join(" → ") ||
                    turn.model_name ||
                    turn.model_id ||
                    "未记录"}
                </dd>
              </div>
              <div>
                <dt>思考</dt>
                <dd>{reasoningLabel}</dd>
              </div>
              <div>
                <dt>权限</dt>
                <dd>
                  {turn.permission
                    ? permissionLabels[turn.permission.mode]
                    : "未记录"}
                </dd>
              </div>
            </dl>
          </section>
        </>
      ) : (
        <section className="overview-section">
          <p>发送后查看实际执行与本轮配置。</p>
        </section>
      )}
      <section className="overview-section">
        <div className="overview-label">
          当前对话上下文
          <button onClick={onContext} disabled={!chat}>
            查看
            <ChevronRight size={13} />
          </button>
        </div>
        {context ? (
          <>
            <div className="overview-usage">
              <strong>
                {occupancy === undefined ? "—" : Math.round(occupancy)}
                <small>%</small>
              </strong>
              <span>
                {context.last_request
                  ? (context.window.tokens
                      ? context.last_request.estimated_input_tokens
                      : context.last_request.bytes
                    ).toLocaleString()
                  : "待请求"}{" "}
                /{" "}
                {(
                  context.window.tokens || context.window.byte_limit
                ).toLocaleString()}{" "}
                {context.window.tokens ? "tokens" : "字节保护"}
              </span>
            </div>
            <progress
              aria-label="上下文占用"
              max={100}
              value={Math.min(100, occupancy || 0)}
            />
            <p>
              {context.automatic
                ? `自动压缩 ${context.window.automatic_percent}% 触发`
                : "自动压缩未开启"}
              {context.automatic_paused ? " · 已暂停" : ""}
              {context.compaction_state === "running" ? " · 压缩中" : ""}
            </p>
          </>
        ) : (
          <p>
            {contextError
              ? "上下文状态暂不可用，点击查看重试。"
              : chat
                ? "正在读取上下文…"
                : "新对话尚无历史。"}
          </p>
        )}
      </section>
      <section className="overview-section">
        <div className="overview-label">本轮可用能力</div>
        {groups.map(({ id, name, Icon, count }) => (
          <button
            key={id}
            className="capability-summary-row"
            onClick={() => onSettings(id)}
            title={`管理${name}，本轮记录保持不变`}
          >
            <Icon size={15} />
            <span>{name}</span>
            <span className="capability-count">
              {turn
                ? selection || id === "tools"
                  ? `${count || 0} 项`
                  : "未记录"
                : "待发送"}
            </span>
            <ChevronRight size={12} />
          </button>
        ))}
        <p>实际参与情况见执行链条。</p>
      </section>
      <div className="overview-workspace">
        <Folder size={15} />
        <span title={runtime.environment?.workspace}>
          {runtime.environment?.workspace?.split(/[\\/]/).pop() || "工作区"}
        </span>
      </div>
    </>
  );
  return (
    <>
      <aside
        className={`turn-overview ${visible ? "" : "overview-hidden"}`}
        aria-label="本轮概览"
      >
        {content}
      </aside>
      <Dialog
        open={mobile}
        onOpenChange={(open) => {
          if (!open) onMobileClose();
        }}
      >
        <DialogContent className="overview-dialog">
          <DialogHeader className="sr-only">
            <DialogTitle>本轮概览</DialogTitle>
            <DialogDescription>
              当前选择的轮次、执行动态与上下文。
            </DialogDescription>
          </DialogHeader>
          {content}
        </DialogContent>
      </Dialog>
    </>
  );
}
