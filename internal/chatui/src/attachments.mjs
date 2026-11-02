import { escapeHTML as esc } from './render.mjs';

export const inputAccept = '.txt,.md,.json,.csv,.go,.py,.js,.mjs,.ts,.tsx,.jsx,.html,.css,.xml,.yaml,.yml,.toml,.ini,.sh,.ps1,.rs,.c,.h,.cpp,.sql,.log,image/png,image/jpeg';
export function attachmentDraft(raw) {
  try {
    const value = typeof raw === 'string' ? JSON.parse(raw) : raw;
    return Array.isArray(value) ? value.filter(ref => /^[0-9a-f]{32}$/.test(ref?.id) && typeof ref.name === 'string' && Number.isSafeInteger(ref.bytes) && ref.bytes >= 0).slice(0, 8) : [];
  } catch { return []; }
}
export function uploadProblem(files, existing = [], policy = {}) {
  if (files.length + existing.length > (policy.max_files || 8)) return '每次最多添加 8 个附件。';
  if (files.some(file => file.size > (policy.max_input_bytes || 8 * 1024 ** 2))) return '单个附件超过输入大小上限。';
  if ([...files.map(file => file.size), ...existing.map(ref => ref.bytes)].reduce((a, b) => a + b, 0) > (policy.max_batch_bytes || 32 * 1024 ** 2)) return '附件总大小超过本次提交上限。';
  return '';
}
export function attachmentCards(refs, chat, { draft = false } = {}) {
  return (refs || []).map(ref => `<div class="attachment-card"><span><strong>${esc(ref.name)}</strong><small>${ref.bytes.toLocaleString()} 字节${ref.public ? ' · 此空间可见' : ''}</small></span>${ref.unavailable ? '<span>已删除或不可用</span>' : draft ? `<button type="button" class="text-action" data-attachment-remove="${esc(ref.id)}" aria-label="移除 ${esc(ref.name)}">移除</button>` : `<button type="button" class="text-action" data-attachment-download="${esc(ref.id)}" data-attachment-chat="${esc(chat)}" data-attachment-name="${esc(ref.name)}">下载</button>${['image/png', 'image/jpeg'].includes(ref.mime) ? `<button type="button" class="text-action" data-attachment-preview="${esc(ref.id)}" data-attachment-chat="${esc(chat)}">查看图片</button>` : ''}`}</div>`).join('');
}
