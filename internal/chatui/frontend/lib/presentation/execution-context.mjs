export function activityTime(activity, now = Date.now()) {
  if (Number.isFinite(activity.duration_ms))
    return `${(Math.max(0, activity.duration_ms) / 1000).toFixed(2)}s`;
  const started = Date.parse(activity.started_at || "");
  if (activity.status === "dispatched" && Number.isFinite(started))
    return `运行 ${Math.max(0, Math.floor((now - started) / 1000))}s`;
  return "耗时未记录";
}

export function windowText(context) {
  if (!context?.window) return "上下文尚未核算";
  const w = context.window,
    u = context.last_request;
  const declared = w.tokens
    ? `声明窗口 ${w.tokens} tokens（${w.source}）`
    : "模型 token 窗口未知，使用本地字节预算";
  const usage = u
    ? `最近准备的请求：估算 ${u.estimated_input_tokens} tokens，${u.bytes} 字节，占用 ${u.occupancy_percent}%；输出预留 ${u.window.output_reserve_tokens} tokens${u.newer_history_available ? "；已有更新消息，非当前完整请求" : ""}`
    : "尚无已装配请求；下次派发前核算";
  const reported = context.last_reported_usage
    ? `上次 Provider 实际返回：输入 ${context.last_reported_usage.input_tokens} / 输出 ${context.last_reported_usage.output_tokens} tokens`
    : "尚无 Provider 实际用量";
  return `${declared}；本地请求上限 ${w.byte_limit} 字节；自动触发 ${w.automatic_percent}%。\n${usage}\n${reported}`;
}

export function inputDisposition(busy, action) {
  if (action === "queue") return "queue";
  if (action === "steer") return busy ? "steer" : "send";
  return busy ? "queue" : "send";
}

export function inputLabel(input) {
  if (input.status === "pending")
    return input.action === "queue" ? "等待发送" : "引导已收到，等待处理";
  if (input.status === "adopted")
    return input.action === "queue" ? "已发送" : "已用于当前任务";
  if (input.status === "cancelled") return "已取消";
  if (input.status === "target_ended") return "当前任务已结束，消息已保留";
  return input.status;
}
