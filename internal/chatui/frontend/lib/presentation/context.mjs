export function summaryByteLimit(value) {
  const text = String(value ?? "").trim();
  if (!/^\d+$/.test(text))
    throw new Error("摘要上限需要是 1 至 16384 的整数。");
  const bytes = Number(text);
  if (!Number.isSafeInteger(bytes) || bytes < 1 || bytes > 16384)
    throw new Error("摘要上限需要是 1 至 16384 的整数。");
  return bytes;
}

export function contextActions(context, busy = false) {
  const blocked = !context || busy || Boolean(context.blocked);
  const saved = context?.status === "current" || context?.status === "stale";
  return {
    compact: !blocked && context.status !== "stale",
    refresh: !blocked && saved,
    clear: !blocked && saved,
  };
}

export function contextStatus(context) {
  if (!context) return "读取中";
  if (context.status === "stale") return "摘要已过期";
  if (!context.summary) return "尚无摘要";
  return `已覆盖 ${Number(context.summary.source_count) || 0} 条记录 · 摘要 ${new TextEncoder().encode(context.summary.text || "").length} 字节`;
}

export function contextStopReason(code, fallback = "") {
  const reasons = {
    output_limit:
      "模型达到单次输出上限，回复尚未完成。可继续，或在模型设置中调整输出额度与思考选项。",
    context_structure_invalid:
      "工具调用与结果的上下文结构不完整。原记录已保留，请查看执行记录；调整额度不能修复结构问题。",
    context_window_full:
      "当前请求超过模型输入窗口。请检查占用来源，手动压缩或调整模型窗口。",
    context_payload_limit:
      "当前请求超过本地请求字节保护，尚未发送给模型。这不代表厂商模型窗口已满。",
    context_history_read_limit:
      "未压缩历史超过本地读取保护。请手动压缩历史；原始记录保留。",
    context_fixed_input:
      "固定规则或工具定义超过当前预算，压缩旧历史不能解决。请检查启用能力与模型设置。",
    context_current_input:
      "当前输入或必要附件超过预算。请缩小输入，或调整模型设置。",
    summary_limit:
      "摘要在修复后仍超过保存上限。原摘要保留，请调整摘要目标后重试。",
    summary_incomplete:
      "模型未返回完整摘要。原摘要保留，请检查辅助思考和输出额度。",
    summary_no_progress:
      "本次压缩没有减少有效占用，已保留原摘要。请检查主要占用来源。",
  };
  return reasons[code] || fallback;
}

export function contextScopeLabel(scope) {
  return (
    {
      full: "完整授权视图",
      public_text: "公开文本视图",
      channel_public: "渠道公开视图",
    }[scope] || "当前授权视图"
  );
}
