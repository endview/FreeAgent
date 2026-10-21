import { createIcons, ClipboardPaste, Copy, Link2, Scissors, TextSelect, Pin } from 'lucide';

const icons = { ClipboardPaste, Copy, Link2, Scissors, TextSelect, Pin };
let reportedBusy = false;
let menu;
let workspaceOptions = {};
const attentionRuns = new Set();

export function desktopMode(scope = globalThis) {
  return typeof scope.faOpenExternal === 'function';
}

export function allowedExternalURL(raw) {
  try {
    const value = new URL(String(raw), globalThis.document?.baseURI || 'http://127.0.0.1/');
    if (value.username || value.password) return false;
    if (value.protocol === 'mailto:') return Boolean(value.pathname);
    return ['http:', 'https:'].includes(value.protocol) && Boolean(value.hostname);
  } catch {
    return false;
  }
}

export function editableTarget(target) {
  if (!target || typeof target.matches !== 'function') return null;
  if (target.matches('textarea')) return target;
  if (!target.matches('input')) return null;
  const type = (target.type || 'text').toLowerCase();
  return ['text', 'search', 'email', 'url', 'tel', 'number'].includes(type) ? target : null;
}

function hasSelection(target) {
  return Boolean(target && typeof target.selectionStart === 'number' &&
    target.selectionStart !== target.selectionEnd);
}

export function menuAvailability(target, selection = '') {
  const editable = editableTarget(target);
  const inputSelection = hasSelection(editable);
  const link = typeof target?.closest === 'function' ? target.closest('a[href]') : null;
  const password = Boolean(target?.matches?.('input') &&
    (target.type || 'text').toLowerCase() === 'password');
  return {
    cut: inputSelection,
    copy: !password && (inputSelection || Boolean(selection)),
    paste: Boolean(editable),
    selectAll: true,
    copyLink: Boolean(link),
  };
}

function callBinding(name, ...args) {
  const binding = globalThis[name];
  if (typeof binding !== 'function') return Promise.resolve(undefined);
  try {
    return Promise.resolve(binding(...args));
  } catch (error) {
    return Promise.reject(error);
  }
}

export function setDesktopBusy(value) {
  const next = Boolean(value);
  if (!desktopMode() || next === reportedBusy) return;
  reportedBusy = next;
  callBinding('faSetBusy', next).catch(() => {});
}

export function notifyDesktopAttention(runID) {
  if (!desktopMode() || document.hasFocus() || !runID || attentionRuns.has(runID)) return;
  attentionRuns.add(runID);
  callBinding('faAttention').catch(() => {});
}

export function openDesktopExternal(raw) {
  if (!desktopMode() || !allowedExternalURL(raw)) return false;
  callBinding('faOpenExternal', String(raw)).catch((error) => {
    workspaceOptions.onError?.(error?.message || '链接无法打开。');
  });
  return true;
}

export async function selectDesktopFolder(current = '') {
  if (!desktopMode()) return '';
  try {
    return String(await callBinding('faSelectFolder', String(current || '')) || '');
  } catch (error) {
    workspaceOptions.onError?.(error?.message || '文件夹选择失败。');
    return '';
  }
}

export async function selectDesktopFile(current = '') {
  if (!desktopMode()) return '';
  try {
    return String(await callBinding('faSelectFile', String(current || '')) || '');
  } catch (error) {
    workspaceOptions.onError?.(error?.message || '文件选择失败。');
    return '';
  }
}

function clipboardError() {
  workspaceOptions.onError?.('无法访问剪贴板。');
}

function selectedText(target) {
  const editable = editableTarget(target);
  if (hasSelection(editable)) return editable.value.slice(editable.selectionStart, editable.selectionEnd);
  return globalThis.window?.getSelection?.()?.toString() || '';
}

async function copyText(raw) {
  try {
    await navigator.clipboard.writeText(raw);
  } catch {
    clipboardError();
  }
}

async function cutText(target) {
  if (!hasSelection(target)) return;
  const start = target.selectionStart, end = target.selectionEnd;
  try {
    await navigator.clipboard.writeText(target.value.slice(start, end));
    target.setRangeText('', start, end, 'start');
    target.dispatchEvent(new Event('input', { bubbles: true }));
  } catch {
    clipboardError();
  }
}

async function pasteText(target) {
  if (!target) return;
  try {
    const text = await navigator.clipboard.readText();
    const start = target.selectionStart ?? target.value.length;
    const end = target.selectionEnd ?? start;
    target.setRangeText(text, start, end, 'end');
    target.dispatchEvent(new Event('input', { bubbles: true }));
  } catch {
    clipboardError();
  }
}

function selectAll(target) {
  const editable = editableTarget(target);
  if (editable) {
    editable.focus();
    editable.select();
    return;
  }
  const root = typeof target?.closest === 'function' ? target.closest('.message, .markdown') : null;
  if (!root) return;
  const range = globalThis.document.createRange();
  range.selectNodeContents(root);
  const selection = globalThis.window.getSelection();
  selection.removeAllRanges();
  selection.addRange(range);
}

function menuHTML(availability, chat) {
  const item = (action, iconName, label, enabled) =>
    `<button type="button" role="menuitem" data-menu-action="${action}" ${enabled ? '' : 'disabled'}>${`<i data-lucide="${iconName}"></i>`}<span>${label}</span></button>`;
  const items = [
    item('cut', 'scissors', '剪切', availability.cut),
    item('copy', 'copy', '复制', availability.copy),
    item('paste', 'clipboard-paste', '粘贴', availability.paste),
    item('selectAll', 'text-select', '全选', availability.selectAll),
    item('copyLink', 'link-2', '复制链接', availability.copyLink),
  ];
  if (chat) items.push(item('pinChat', 'pin', chat.pinned ? '取消置顶会话' : '置顶会话', true));
  return items.join('');
}

function closeMenu() {
  menu?.remove();
  menu = null;
}

function openMenu(event) {
  closeMenu();
  const target = event.target instanceof Element ? event.target : null;
  const availability = menuAvailability(target, window.getSelection()?.toString() || '');
  const chatID = target?.closest?.('[data-chat]')?.dataset.chat || '';
  const chat = chatID ? workspaceOptions.getChatContext?.(chatID) : null;
  menu = document.createElement('div');
  menu.className = 'desktop-context-menu';
  menu.setAttribute('role', 'menu');
  menu.innerHTML = menuHTML(availability, chat);
  document.body.append(menu);
  createIcons({ icons, attrs: { 'aria-hidden': 'true', 'stroke-width': 1.7 } });
  const width = menu.offsetWidth, height = menu.offsetHeight;
  const left = Math.min(event.clientX, Math.max(4, window.innerWidth - width - 4));
  const top = Math.min(event.clientY, Math.max(4, window.innerHeight - height - 4));
  menu.style.left = `${left}px`;
  menu.style.top = `${top}px`;
  menu.dataset.target = target?.closest('a[href]')?.href || '';
  menu.dataset.chat = chatID;
  menu.dataset.pinned = String(Boolean(chat?.pinned));
  menu._target = target;
  menu.querySelector('[role="menuitem"]:not(:disabled)')?.focus();
}

async function runMenuAction(action) {
  if (!menu) return;
  const target = menu._target;
  const editable = editableTarget(target);
  switch (action) {
    case 'cut': await cutText(editable); break;
    case 'copy': await copyText(selectedText(target)); break;
    case 'paste': await pasteText(editable); break;
    case 'selectAll': selectAll(target); break;
    case 'copyLink': await copyText(menu.dataset.target); break;
    case 'pinChat': await workspaceOptions.onChatPin?.(menu.dataset.chat, menu.dataset.pinned !== 'true'); break;
  }
  closeMenu();
}

function showWorkspaceHelp(path) {
  let dialog = document.querySelector('#workspace-help');
  if (!dialog) {
    dialog = document.createElement('dialog');
    dialog.id = 'workspace-help';
    dialog.innerHTML = `<form method="dialog"><header><h2>更换工作区</h2><button class="icon" value="cancel" aria-label="关闭">×</button></header>
      <p>浏览器模式不能直接更换工作区。请先关闭 FreeAgent，再在命令行执行：</p>
      <pre id="workspace-command"></pre><footer><button type="button" class="icon" id="workspace-copy" title="复制命令" aria-label="复制命令"><i data-lucide="copy"></i></button><button class="primary" value="cancel">关闭</button></footer></form>`;
    document.body.append(dialog);
    createIcons({ icons, attrs: { 'aria-hidden': 'true', 'stroke-width': 1.7 } });
    dialog.querySelector('#workspace-copy').addEventListener('click', () => copyText(dialog.querySelector('#workspace-command').textContent));
  }
  const escaped = String(path || '').replaceAll('"', '\\"');
  dialog.querySelector('#workspace-command').textContent = `freeagent.exe config workspace --path "${escaped}"`;
  dialog.showModal();
}

export function installDesktopBridge(options = {}) {
  workspaceOptions = options;
  document.addEventListener('contextmenu', (event) => {
    if (!desktopMode()) return;
    event.preventDefault();
    openMenu(event);
  });
  document.addEventListener('click', (event) => {
    if (menu && !menu.contains(event.target)) closeMenu();
  });
  document.addEventListener('keydown', (event) => {
    if (!menu) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      closeMenu();
      return;
    }
    const items = [...menu.querySelectorAll('[role="menuitem"]:not(:disabled)')];
    const current = items.indexOf(document.activeElement);
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const offset = event.key === 'ArrowDown' ? 1 : -1;
      items[(current + offset + items.length) % items.length]?.focus();
    } else if (event.key === 'Enter' && current >= 0) {
      event.preventDefault();
      runMenuAction(items[current].dataset.menuAction);
    }
  });
  document.addEventListener('click', (event) => {
    const anchor = event.target instanceof Element ? event.target.closest('a[href]') : null;
    if (!anchor || !desktopMode()) return;
    if (event.button === 0 || event.button === 1 || event.ctrlKey || event.shiftKey) {
      event.preventDefault();
      event.stopImmediatePropagation();
      openDesktopExternal(anchor.href);
    }
  }, true);
  document.addEventListener('auxclick', (event) => {
    if (event.button !== 1 || !desktopMode()) return;
    const anchor = event.target instanceof Element ? event.target.closest('a[href]') : null;
    if (!anchor) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    openDesktopExternal(anchor.href);
  }, true);
  document.addEventListener('dragover', (event) => {
    if (desktopMode()) event.preventDefault();
  });
  document.addEventListener('drop', (event) => {
    if (desktopMode()) event.preventDefault();
  });
  document.addEventListener('click', async (event) => {
    const button = event.target instanceof Element ? event.target.closest('#workspace-change') : null;
    if (!button) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (options.getBusy?.()) {
      options.onError?.('请先停止运行中的任务。');
      return;
    }
    if (!desktopMode()) {
      showWorkspaceHelp(options.getWorkspace?.());
      return;
    }
    try {
      const result = await callBinding('faChangeWorkspace');
      if (result?.changed) options.onChanged?.();
    } catch (error) {
      options.onError?.(error?.message || '工作区没有更换。');
    }
  }, true);
}
