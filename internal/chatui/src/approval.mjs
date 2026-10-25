import { escapeHTML as esc } from './render.mjs';

const MAX_DIFF_LINES = 2000;

function lines(value) {
  const text = String(value ?? '');
  return text ? text.split(/\r\n|\r|\n/) : [];
}

export function diffLines(oldText, newText) {
  const before = lines(oldText);
  const after = lines(newText);
  if (before.length > MAX_DIFF_LINES || after.length > MAX_DIFF_LINES) {
    return { bounded: true, old: String(oldText ?? ''), new: String(newText ?? '') };
  }
  const width = after.length + 1;
  const table = Array.from({ length: before.length + 1 }, () => new Uint32Array(width));
  for (let left = before.length - 1; left >= 0; left--) {
    for (let right = after.length - 1; right >= 0; right--) {
      table[left][right] = before[left] === after[right]
        ? table[left + 1][right + 1] + 1
        : Math.max(table[left + 1][right], table[left][right + 1]);
    }
  }
  const result = [];
  let left = 0, right = 0;
  while (left < before.length && right < after.length) {
    if (before[left] === after[right]) {
      result.push({ kind: 'same', text: before[left++] });
      right++;
    } else if (table[left + 1][right] >= table[left][right + 1]) {
      result.push({ kind: 'remove', text: before[left++] });
    } else {
      result.push({ kind: 'add', text: after[right++] });
    }
  }
  while (left < before.length) result.push({ kind: 'remove', text: before[left++] });
  while (right < after.length) result.push({ kind: 'add', text: after[right++] });
  return { bounded: false, lines: result };
}

function diffHTML(oldText, newText) {
  const diff = diffLines(oldText, newText);
  if (diff.bounded) {
    return `<div class="approval-block"><pre class="approval-old">${esc(diff.old)}</pre><pre class="approval-new">${esc(diff.new)}</pre></div>`;
  }
  return `<pre class="approval-diff">${diff.lines.map((line) =>
    `<span class="diff-${line.kind}">${line.kind === 'remove' ? '-' : line.kind === 'add' ? '+' : ' '}${esc(line.text)}</span>`).join('\n')}</pre>`;
}

function fieldsHTML(fields = {}) {
  return Object.entries(fields).map(([key, value]) =>
    `<li><strong>${esc(key)}</strong><span>${esc(value)}</span></li>`).join('');
}

export function approvalItemsHTML(items = []) {
  return items.map((item) => {
    const kind = item.kind || 'operation';
    const target = item.target || '';
    if (kind === 'file_edit' || kind === 'file_create' || kind === 'file_delete') {
      const oldText = kind === 'file_create' ? '' : item.old;
      const newText = kind === 'file_delete' ? '' : item.new;
      return `<article class="approval-item file-approval"><header><strong>${esc(target)}</strong>${item.sha256 ? `<code>${esc(item.sha256.slice(0, 8))}</code>` : ''}</header>${item.fields ? `<ul class="approval-fields">${fieldsHTML(item.fields)}</ul>` : ''}${diffHTML(oldText, newText)}</article>`;
    }
    const warning = ['command', 'http', 'mcp'].includes(kind)
      ? '<p class="warning">非沙箱：此操作可能产生外部作用，清理不会撤销已发生的作用。</p>' : '';
    return `<article class="approval-item"><header><strong>${esc(kind)}</strong><span>${esc(target)}</span></header>${warning}${item.fields ? `<ul class="approval-fields">${fieldsHTML(item.fields)}</ul>` : ''}</article>`;
  }).join('');
}

export function approvalHTML(confirmation, options = {}) {
  if (confirmation?.control_operation_id) return controlApprovalHTML(confirmation, options.control);
  const items = confirmation?.items || [];
  const title = options.title || '等待确认';
  const target = items[0]?.target || confirmation?.tool || '运行操作';
  const approveLabel = items.some((item) => ['command', 'http', 'mcp'].includes(item.kind))
    ? '批准执行' : '批准变更';
  const body = items.length ? `<div class="approval-items">${approvalItemsHTML(items)}</div>`
    : `<pre class="approval-raw">${esc(confirmation?.preview || '')}</pre>`;
  return `<section class="approval-card" aria-labelledby="approval-card-title">
    <header tabindex="-1"><div><h3 id="approval-card-title">${esc(title)}</h3><span>${esc(target)}</span></div><span class="approval-action">需要确认</span></header>
    <div class="approval-tabs"><span class="selected">变更</span><button type="button" class="text-action" data-action="approval-raw">原文</button></div>
    <div class="approval-body">${body}</div>
    <footer><button class="text-action" data-action="reject">拒绝</button><button class="primary" data-action="approve">${esc(approveLabel)}</button></footer>
  </section>`;
}

const CONTROL_RISK_LABELS = {
  ordinary: '普通设置',
  authority: '扩大权限',
  destructive: '删除内容',
  irreversible: '不可撤销',
};

// The server accepts the code with or without the separator.
export function formatControlCode(code) {
  const value = String(code || '').replace(/[\s-]/g, '').toUpperCase();
  return value.length > 5 ? `${value.slice(0, 5)}-${value.slice(5)}` : value;
}

function controlExpiry(value) {
  const expires = value ? new Date(value) : null;
  if (!expires || Number.isNaN(expires.getTime())) return '';
  return `${expires.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} 前有效`;
}

// A model control proposal is decided with the owner-rendered code; the card
// shows it so the same code can be entered from the CLI.
function controlApprovalHTML(confirmation, control) {
  const fields = confirmation?.items?.[0]?.fields || {};
  const code = control?.operation_id === confirmation.control_operation_id ? formatControlCode(control.code) : '';
  const expiry = code ? controlExpiry(control.expires_at) : '';
  return `<section class="approval-card control-approval" aria-labelledby="approval-card-title">
    <header tabindex="-1"><div><h3 id="approval-card-title">设置变更</h3><span>${esc(fields.kind || confirmation.tool || '')}</span></div><span class="approval-action">${esc(CONTROL_RISK_LABELS[fields.risk] || '需要确认')}</span></header>
    <div class="approval-body"><p class="control-preview">${esc(confirmation.preview || '')}</p>
      <div class="control-code"><span>确认码</span><code>${esc(code || '加载中')}</code>${expiry ? `<span>${esc(expiry)}</span>` : ''}</div>
      <p class="subtle-note">应用后从下一次运行开始生效。</p></div>
    <footer><button class="text-action" data-action="reject">拒绝</button><button class="primary" data-action="approve"${code ? '' : ' disabled'}>应用变更</button></footer>
  </section>`;
}

export function approvalRawHTML(confirmation) {
  return `<section class="approval-card approval-raw-card" aria-labelledby="approval-card-title">
    <header tabindex="-1"><div><h3 id="approval-card-title">审批原文</h3><span>${esc(confirmation?.tool || '运行操作')}</span></div><button class="text-action" data-action="approval-items">查看结构化变更</button></header>
    <div class="approval-body"><pre class="approval-raw">${esc(confirmation?.preview || '')}</pre><button class="text-action" data-copy="${encodeURIComponent(confirmation?.digest || '')}" title="复制摘要" aria-label="复制摘要">复制摘要</button></div>
  </section>`;
}
