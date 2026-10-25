import { escapeHTML as esc } from './render.mjs';

export function commandState(record) {
  return ({ running: '运行中', stopping: '正在停止', completed: '已完成', failed: '失败',
    stopped: '已停止', timed_out: '期限已到', unknown: '结果未知' })[record.state] || record.state;
}
export function commandRows(records, icon) {
  if (!records.length) return '<p class="subtle-note">没有后台命令</p>';
  return records.map((r) => `<article class="command-row" data-command-id="${esc(r.id)}">
    <div><strong>${esc(r.receipt?.command || r.id)}</strong><span>${esc(commandState(r))} · ${esc(r.mode)} · ${esc(r.expires_at)}</span><code>${esc(r.id)}</code></div>
    <div class="command-actions"><button class="icon" data-action="command-poll" data-id="${esc(r.id)}" title="读取输出" aria-label="读取输出">${icon('scroll-text')}</button>${['running', 'stopping'].includes(r.state) ? `<button class="icon" data-action="command-stop" data-id="${esc(r.id)}" title="停止命令" aria-label="停止命令">${icon('square')}</button>` : ''}</div>
  </article>`).join('');
}
export function commandOutput(record) {
  return `${commandState(record)} · cleanup=${record.receipt?.cleanup || ''}\n${record.output_gap ? '[前段输出已丢弃或不完整]\n' : ''}${(record.output || []).map((part) => part.text).join('')}`;
}
