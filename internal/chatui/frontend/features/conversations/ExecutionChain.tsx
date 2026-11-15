import { useEffect, useMemo, useRef, useState } from "react";
import {
  Check,
  ChevronDown,
  CircleHelp,
  CirclePause,
  Clock3,
  LoaderCircle,
  RotateCw,
  TriangleAlert,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api/client";
import type { Activity, Turn } from "@/lib/api/types";
import {
  activityLabels,
  activityState,
  activityTitle,
  attentionState,
} from "@/lib/presentation/activity.mjs";

export function ActivityIcon({ status }: { status: string }) {
  const Icon =
    status === "waiting"
      ? CirclePause
      : status === "unknown"
        ? CircleHelp
        : status === "failed"
          ? TriangleAlert
          : status === "running"
            ? LoaderCircle
            : status === "done" || status === "resolved"
              ? Check
              : status === "retried"
                ? RotateCw
                : Clock3;
  return (
    <Icon
      size={15}
      className={status === "running" ? "spin" : ""}
      aria-hidden="true"
    />
  );
}

function Record({
  item,
  chat,
  turnState,
  focus,
}: {
  item: Activity;
  chat: string;
  turnState: string;
  focus: boolean;
}) {
  const [expanded, setExpanded] = useState(focus),
    [raw, setRaw] = useState<string | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const status = activityState(item, turnState);
  return (
    <li>
      <details
        className={`chain-record activity-${status}`}
        open={expanded}
        onToggle={(event) => setExpanded(event.currentTarget.open)}
        data-activity={item.key}
      >
        <summary>
          <ActivityIcon status={status} />
          <span className="chain-title">{activityTitle(item)}</span>
          <span className="activity-status">{activityLabels[status]}</span>
          <time>
            {item.duration_ms === undefined
              ? ""
              : `${(item.duration_ms / 1000).toFixed(1)}s`}
          </time>
          <ChevronDown size={13} />
        </summary>
        {expanded && (
          <div className="chain-record-body">
            <dl className="run-facts">
              <div>
                <dt>操作</dt>
                <dd>{item.name || item.kind}</dd>
              </div>
              {item.model_name && (
                <div>
                  <dt>实际型号</dt>
                  <dd>{item.model_name}</dd>
                </div>
              )}
              {item.target && (
                <div>
                  <dt>目标</dt>
                  <dd>{item.target}</dd>
                </div>
              )}
              {item.started_at && (
                <div>
                  <dt>开始</dt>
                  <dd>{new Date(item.started_at).toLocaleString()}</dd>
                </div>
              )}
            </dl>
            {item.requested_reasoning && (
              <p>
                请求思考：
                {item.requested_reasoning.effort ||
                  item.requested_reasoning.mode ||
                  "Provider 默认"}
                （请求值）
              </p>
            )}
            {item.usage && (
              <p>
                输入 {item.usage.input_tokens} / 输出 {item.usage.output_tokens}{" "}
                token；推理 {item.usage.reasoning_tokens ?? "未回显"}
              </p>
            )}
            {item.replacement && <p>此尝试已有后续重试，原失败记录保留。</p>}
            {item.resolution && <p>结果已经核实，原始证据保留。</p>}
            {item.extensions?.map((use, index) => (
              <p key={index}>
                已附加上下文：{use.label || use.name} ·{" "}
                {use.bytes.toLocaleString()} 字节
                {use.version ? ` · ${use.version}` : ""}
              </p>
            ))}
            {(raw !== null || item.text) && <pre>{raw ?? item.text}</pre>}
            {item.truncated && raw === null && (
              <Button
                variant="outline"
                disabled={busy}
                onClick={async () => {
                  setBusy(true);
                  setError("");
                  try {
                    const data = await api<{ raw: string }>(
                      `chats/${chat}/raw?kind=activity&key=${encodeURIComponent(item.key)}`,
                    );
                    setRaw(data.raw);
                  } catch (e) {
                    setError(e instanceof Error ? e.message : "读取失败");
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                {busy ? "正在读取…" : "读取完整结果"}
              </Button>
            )}
            {item.receipts?.map((receipt, index) => (
              <p key={index}>
                {receipt.kind} · {receipt.status}
                {receipt.exit_code !== undefined
                  ? ` · 退出码 ${receipt.exit_code}`
                  : ""}
                {receipt.path ? ` · ${receipt.path}` : ""}
                {receipt.host_escape_hatch ? " · host" : ""}
                {receipt.detail ? ` · ${receipt.detail}` : ""}
              </p>
            ))}
            {error && (
              <p className="inline-error" role="alert">
                {error}
              </p>
            )}
          </div>
        )}
      </details>
    </li>
  );
}
interface ActivityPage {
  run_id: string;
  items: Activity[];
  before?: string;
  has_earlier: boolean;
  total: number;
}
export function ExecutionChain({
  chat,
  turn,
  focus = "",
  onClose,
  onInspect,
}: {
  chat: string;
  turn: Turn;
  focus?: string;
  onClose: () => void;
  onInspect: () => void;
}) {
  const [older, setOlder] = useState<Activity[]>([]),
    [filter, setFilter] = useState("all"),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [page, setPage] = useState<ActivityPage | null>(null);
  const scroll = useRef<HTMLDivElement>(null);
  const trigger = useRef(document.activeElement as HTMLElement | null);
  useEffect(() => {
    const controller = new AbortController();
    api<ActivityPage>(
      `chats/${chat}/activity?run_id=${encodeURIComponent(turn.key)}`,
      undefined,
      undefined,
      controller.signal,
    )
      .then((value) => {
        if (!controller.signal.aborted) {
          setPage(value);
          setOlder((previous) => [
            ...new Map(
              [...previous, ...value.items].map((item) => [item.key, item]),
            ).values(),
          ]);
        }
      })
      .catch((e) => {
        if (!controller.signal.aborted) setError(e.message);
      });
    return () => controller.abort();
  }, [chat, turn.key]);
  useEffect(() => {
    if (!turn.attention_activity?.length) return;
    setOlder((previous) => [
      ...new Map(
        [...previous, ...turn.attention_activity!].map((item) => [
          item.key,
          item,
        ]),
      ).values(),
    ]);
  }, [turn.attention_activity]);
  const items = useMemo(
    () =>
      [
        ...new Map(
          [...older, ...(turn.attention_activity || []), ...turn.activity].map(
            (item) => [item.key, item],
          ),
        ).values(),
      ].sort((a, b) => (a.sequence || 0) - (b.sequence || 0)),
    [older, turn.activity, turn.attention_activity],
  );
  const attention = items.filter((item) =>
    attentionState(activityState(item, turn.state)),
  );
  const displayed = filter === "attention" ? attention : items;
  const more = page?.has_earlier ?? Boolean(turn.activity_has_earlier);
  async function loadMore() {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const value = await api<ActivityPage>(
        `chats/${chat}/activity?run_id=${encodeURIComponent(turn.key)}&before=${encodeURIComponent(page?.before || turn.activity[0]?.key || "")}`,
      );
      const el = scroll.current,
        height = el?.scrollHeight || 0,
        top = el?.scrollTop || 0;
      setOlder((previous) => [...value.items, ...previous]);
      setPage(value);
      requestAnimationFrame(() => {
        if (el) el.scrollTop = top + el.scrollHeight - height;
      });
    } catch (e) {
      setError(e instanceof Error ? e.message : "读取失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent
        className="execution-chain-dialog"
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          if (trigger.current?.isConnected)
            trigger.current.focus({ preventScroll: true });
        }}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          const row = focus
            ? scroll.current?.querySelector<HTMLElement>(
                `[data-activity="${CSS.escape(focus)}"] summary`,
              )
            : null;
          (row || scroll.current)?.focus();
          row?.scrollIntoView({ block: "nearest" });
        }}
      >
        <DialogHeader>
          <DialogTitle>完整执行链条</DialogTitle>
          <DialogDescription>
            按执行顺序查看；展开单项可核对结果与耗时。
          </DialogDescription>
        </DialogHeader>
        <div className="chain-toolbar">
          <div role="group" aria-label="链条筛选">
            <button
              aria-pressed={filter === "all"}
              onClick={() => setFilter("all")}
            >
              全部
            </button>
            <button
              aria-pressed={filter === "attention"}
              onClick={() => setFilter("attention")}
            >
              待处理 {attention.length}
              {more ? "+" : ""}
            </button>
          </div>
          <span>已加载 {items.length} 条</span>
        </div>
        <div
          ref={scroll}
          className="chain-scroll"
          tabIndex={-1}
          role="region"
          aria-label="执行链条记录"
        >
          {more && (
            <button className="load-earlier" disabled={busy} onClick={loadMore}>
              {busy ? "正在读取…" : "加载更早记录"}
            </button>
          )}
          {error && (
            <p role="alert" className="inline-error">
              {error}
              <button onClick={loadMore}>重试读取</button>
            </p>
          )}
          <ol className="chain-records">
            {displayed.map((item) => (
              <Record
                key={item.key}
                item={item}
                chat={chat}
                turnState={turn.state}
                focus={focus === item.key}
              />
            ))}
          </ol>
          {!displayed.length && (
            <p className="empty-chain">
              {filter === "attention"
                ? "当前已加载记录中没有待处理项。"
                : "本轮还没有执行记录。"}
            </p>
          )}
        </div>
        <footer className="chain-footer">
          <span>新动态保留当前阅读位置</span>
          {turn.state === "awaiting_approval" && (
            <Button
              variant="outline"
              onClick={() => {
                onClose();
                onInspect();
              }}
            >
              前往确认卡片
            </Button>
          )}
        </footer>
      </DialogContent>
    </Dialog>
  );
}
