// UI projection only: decisions and completion remain owned by Core.
export function activityState(item, turnState = "") {
  if (item.resolution) return "resolved";
  if (item.replacement) return "retried";
  if (item.status === "unknown") return "unknown";
  if (item.is_error || ["failed", "error", "timed_out"].includes(item.status))
    return "failed";
  if (
    ["waiting_confirmation", "awaiting_approval"].includes(item.status) ||
    (turnState === "awaiting_approval" &&
      ["prepared", "pending"].includes(item.status))
  )
    return "waiting";
  if (["dispatched", "running"].includes(item.status)) return "running";
  if (["cancelled", "canceled", "abandoned", "stopped"].includes(item.status))
    return "stopped";
  if (["finished", "completed", "success", "succeeded"].includes(item.status))
    return "done";
  return "pending";
}
export function attentionState(status) {
  return ["waiting", "failed", "unknown"].includes(status);
}
export const activityLabels = {
  waiting: "等待确认",
  failed: "失败",
  unknown: "结果未知",
  running: "运行中",
  done: "已完成",
  stopped: "已停止",
  pending: "待执行",
  resolved: "已核实",
  retried: "已重试",
};
export function activityTitle(item) {
  const titles = {
    model: "生成回答",
    assessment: "选择模型",
    summary: "压缩历史",
    compaction: "压缩历史",
    extension_analysis: "扩展分析",
    file_read: "读取文件",
    file_create: "创建文件",
    file_edit: "编辑文件",
    file_list: "查看文件列表",
    file_search: "搜索文件",
    command_run: "执行命令",
    network_request: "请求网络",
    web_search: "搜索网页",
    memory_read: "读取记忆",
    memory_write: "更新记忆",
  };
  return (
    titles[item.kind] ||
    titles[item.name] ||
    item.name ||
    item.kind ||
    "执行记录"
  );
}
// Preserve arrival order for changes to an existing operation. Loading an old
// page is not a new update; immutable sequence keeps those rows in the past.
export function trackActivityUpdates(
  items,
  previous = { clock: 0, records: {} },
) {
  let clock = Math.max(
    previous.clock,
    ...items.map((item, index) => item.sequence || index + 1),
    0,
  );
  const records = Object.create(null);
  for (const [index, item] of items.entries()) {
    const signature = JSON.stringify([
      item.status,
      item.is_error,
      item.replacement,
      item.resolution,
      item.duration_ms,
      item.text?.length,
    ]);
    const saved = previous.records[item.key];
    records[item.key] = {
      signature,
      updated: !saved
        ? item.sequence || index + 1
        : saved.signature === signature
          ? saved.updated
          : ++clock,
    };
  }
  return { clock, records };
}
export function recentActivity(items, turnState, tracked) {
  return [...new Map(items.map((item) => [item.key, item])).values()]
    .sort(
      (a, b) =>
        Number(attentionState(activityState(b, turnState))) -
          Number(attentionState(activityState(a, turnState))) ||
        (tracked.records[b.key]?.updated || b.sequence || 0) -
          (tracked.records[a.key]?.updated || a.sequence || 0),
    )
    .slice(0, 3);
}
