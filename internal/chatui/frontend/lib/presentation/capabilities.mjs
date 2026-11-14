export function extensionStatus(item) {
  if (!item.available || item.error || item.state === "error")
    return {
      label: "不可用",
      detail: item.error || "请检查扩展配置或运行日志。",
      tone: "error",
    };
  if (!item.enabled)
    return {
      label: "未启用",
      detail: "数据保留，当前不会参与回答。",
      tone: "muted",
    };
  if (item.storage === "postgresql" && !item.storage_ready)
    return {
      label: "待初始化存储",
      detail: "启用已保存，完成存储初始化后才能使用。",
      tone: "warning",
    };
  return {
    label: item.state === "running" ? "进程运行中" : "已启用 · 按需启动",
    detail: "还需在对话中选择；进程待命不代表扩展已关闭。",
    tone: "success",
  };
}

export function characterSelection(choices, selected, defaultID) {
  const id = selected || defaultID || "";
  if (!id) return { id: "", label: "未设置人物", source: "none", ready: false };
  const choice = choices.find((item) => item.id === id);
  return {
    id,
    label: choice?.label || "人物不可用于此空间",
    source: selected ? "conversation" : "default",
    ready: Boolean(choice),
  };
}
