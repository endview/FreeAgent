export function summaryByteLimit(value) {
  const text = String(value ?? '').trim();
  if (!/^\d+$/.test(text)) throw new Error('摘要上限需要是 1 至 16384 的整数。');
  const bytes = Number(text);
  if (!Number.isSafeInteger(bytes) || bytes < 1 || bytes > 16384) throw new Error('摘要上限需要是 1 至 16384 的整数。');
  return bytes;
}

export function contextActions(context, busy = false) {
  const blocked = !context || busy || Boolean(context.blocked);
  const saved = context?.status === 'current' || context?.status === 'stale';
  return { compact: !blocked && context.status !== 'stale', refresh: !blocked && saved, clear: !blocked && saved };
}

export function contextStatus(context) {
  if (!context) return '读取中';
  if (context.status === 'stale') return '摘要已过期';
  if (!context.summary) return '尚无摘要';
  return `已覆盖 ${Number(context.summary.source_count) || 0} 条记录 · 摘要 ${new TextEncoder().encode(context.summary.text || '').length} 字节`;
}
