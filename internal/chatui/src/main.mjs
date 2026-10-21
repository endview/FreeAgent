import { createIcons, Menu, Plus, MessageSquare, ArrowUp, ArrowDown, Square, Play,
  X, ChevronDown, ChevronRight, Copy, Eye, PencilLine, Check, Settings2,
  RefreshCw, PanelLeftClose, CircleAlert, FileCheck2, Terminal, Link2,
  KeyRound, PlugZap, Save, Trash2, Hand, LoaderCircle, CircleCheck, CircleX,
  CircleQuestionMark, TriangleAlert, Search, FolderOpen, ExternalLink, Scissors,
  ClipboardPaste, TextSelect, History, Globe, FileText, FolderTree, FileSearch,
  FilePen, FilePlus, Files, Brain, BookOpen, Bot, WifiOff, ShieldCheck, Database,
  EyeOff, Blocks, ScrollText, Puzzle, FolderPlus, ToggleLeft, ToggleRight, Pin } from 'lucide';
import 'katex/dist/katex.min.css';
import './style.css';
import { renderMarkdown, escapeHTML as esc, splitStable } from './render.mjs';
import { STATES, shouldSend, followsLatest, canSubmit, reduceStream, safeStorage,
  isCurrentSnapshot, restoreRawBodies, providerFailureText, chatTitle, stateMeta,
  reasonText, activityLabel, submitMode, endRisk, evidenceGroups, shortHash, turnSignature,
  groupChats, prioritizePinned, matchChat, DRAWER_QUERY, attentionTitle, permissionMode } from './state.mjs';
import { hasModels, keyDraftChanged, keyFormValue, keyHost, keyStatus, modelDisplay, modelsForKey } from './keys.mjs';
import { nextIndex, modelMenuItems } from './menu.mjs';
import { commandEntries, commandQuery, filterCommands, commandNeedsInput, commandInputDefaults } from './commands.mjs';
import { installDesktopBridge, setDesktopBusy, notifyDesktopAttention, selectDesktopFolder, selectDesktopFile } from './desktop.mjs';
import { approvalHTML, approvalRawHTML } from './approval.mjs';
import { bindLayoutResizers } from './layout.mjs';

const icons = { Menu, Plus, MessageSquare, ArrowUp, ArrowDown, Square, Play, X,
  ChevronDown, ChevronRight, Copy, Eye, PencilLine, Check, Settings2, RefreshCw,
  PanelLeftClose, CircleAlert, FileCheck2, Terminal, Link2, KeyRound, PlugZap, Save,
  Trash2, Hand, LoaderCircle, CircleCheck, CircleX, CircleQuestionMark, TriangleAlert,
  Search, FolderOpen, ExternalLink, Scissors, ClipboardPaste, TextSelect, History, Globe,
  FileText, FolderTree, FileSearch, FilePen, FilePlus, Files, Brain, BookOpen, Bot, WifiOff,
  ShieldCheck, Database, EyeOff, Blocks, ScrollText, Puzzle, FolderPlus, ToggleLeft, ToggleRight, Pin };
const icon = (name) => `<i data-lucide="${name}"></i>`;
const button = (name, label, action, extra = '') =>
  `<button type="button" class="icon" data-action="${action}" title="${label}" aria-label="${label}" ${extra}>${icon(name)}</button>`;
const $ = (selector) => document.querySelector(selector);
const app = $('#app');
const baseDocumentTitle = document.title || 'FreeAgent Chat';
const storage = (() => { try { return localStorage; } catch { return null; } })();
const session = (() => { try { return sessionStorage; } catch { return null; } })();
let key = new URLSearchParams(location.hash.slice(1)).get('key') || safeStorage(session, 'freeagent-key');
if (key) safeStorage(session, 'freeagent-key', key);
history.replaceState(null, '', location.pathname + location.search);

// chat is null while drafting: a new chat exists only after its first send.
const state = { env: null, chats: [], chat: '', drafting: false, creating: false, view: null, turns: [], busy: false,
  elsewhere: false, stopping: false, online: false, following: true, composing: false,
  preview: false, pending: null, streams: new Map(), expanded: new Set(),
  approval: null, approvalControl: null, approvalTab: 'items', approvalNotice: '', approvalLoading: false,
  raw: new Map(), refresh: { current: 0, earlier: 0, generation: 0 },
  streamRevision: 0, epoch: 0, mutation: false, page: 'chat', keys: null, keyProfile: null,
  keyModels: new Map(), workspaceChanging: false, activeChat: '', offlineSince: 0,
  keyProbe: null, keyNotice: '', keyModelQuery: '', keyDrafts: new Map(), keyManualModel: '',
  connectionEver: false, runAnchor: null, runNotice: '', endIntent: 'end', endPrompt: '',
  earlierLoading: false, chatQuery: '', modelMenuOpen: false, modelMenuIndex: -1,
  pendingModel: '', pendingPermission: '', permissionConfirmed: false, permissionMenuOpen: false,
  permissionDialogMode: '',
  pendingCapabilities: null, capabilityMenuOpen: false,
  memory: null, memoryOpen: false, memoryLoading: false,
  mcp: null, mcpServer: '', skills: null, skillName: '', skillDetail: null,
  extensions: null, extensionId: '', capabilityTab: 'mcp',
  commandMenuOpen: false, commandIndex: -1, pendingCommand: null, commandCatalog: [],
  permissions: null, settings: null, permissionPageLoading: false, permissionRootDraft: null, permissionBlockedDraft: null };
let sse, snapshotTimer, streamTimer, runTimer, refreshListTimer, offlineTimer, errorTimer = 0, errorRevision = 0, scrollProgram = false;
let earlierObserver;
let earlierVisible = false;
let desktopBusy = false;
let titleAttention = false;
const statuses = { pending: '待执行', dispatched: '运行中', finished: '已完成',
  failed: '失败', unknown: '结果未知', applied: '已应用', rejected: '已拒绝',
  skipped: '已跳过', not_launched: '未运行', failed_to_start: '未启动',
  completed: '已完成', interrupted: '已中断', timed_out: '超时', conflict: '冲突' };
const statusText = (status) => statuses[status] || status;
const latest = () => state.turns.at(-1);
const chatDraftKey = (id) => `freeagent:${state.env?.environment}:${id}:draft`;
const draftKey = () => state.chat ? chatDraftKey(state.chat) : `freeagent:${state.env?.environment}:draft:new`;
const pendingKey = () => `freeagent:${state.env?.environment}:${state.chat}:submission`;
const selectedKey = () => `freeagent:${state.env?.environment}:selected`;
const hasView = () => !!state.chat || state.drafting;
function setTitleAttention(waiting) {
  titleAttention = Boolean(waiting);
  document.title = attentionTitle(state.view?.conversation?.title || baseDocumentTitle, titleAttention);
}

app.innerHTML = `
  <div class="workspace">
    <div class="scrim" data-action="sidebar"></div>
    <aside class="sidebar" aria-label="会话">
      <div class="brand"><span class="brand-mark" aria-hidden="true">F</span><strong>FreeAgent</strong>${button('panel-left-close', '收起会话', 'sidebar')}</div>
      <button class="new-chat" data-action="new">${icon('plus')}<span>新建对话</span></button>
      <div class="list-label">最近对话</div>
      <label class="chat-search"><span class="sr-only">搜索会话</span>${icon('search')}<input id="chat-search" type="search" placeholder="搜索会话" autocomplete="off"></label>
      <nav id="chats" aria-label="最近对话"></nav>
      <div class="environment"><span class="workspace-label">工作区</span><button id="workspace-change" class="icon" title="更换工作区" aria-label="更换工作区">${icon('folder-open')}</button><span id="workspace-path"></span><span class="local-label">本地</span></div>
      <div class="sidebar-bottom">${button('key-round', 'Key 管理', 'keys')}${button('blocks', '能力管理', 'capabilities')}${button('settings-2', '设置', 'settings')}</div>
    </aside>
    <div class="layout-resizer sidebar-resizer" data-layout-resizer="sidebar" role="separator" aria-orientation="vertical" aria-label="调整会话列表宽度" tabindex="0"></div>
    <main>
      <header class="chat-header">
        ${button('menu', '会话列表', 'sidebar')}
        <div class="heading"><div class="title-wrap"><h1 id="title">FreeAgent</h1><button id="rename-chat" class="icon" data-action="rename" title="重命名会话" aria-label="重命名会话" hidden>${icon('pencil')}</button></div><span id="connection" role="status" aria-live="polite">连接中</span></div>
        <div class="model-label"><button id="model-trigger" type="button" class="model-trigger" aria-haspopup="menu" aria-expanded="false"></button><div id="model-menu" class="model-menu" role="menu" hidden></div></div>
        <div class="permission-label"><button id="permission-trigger" type="button" class="model-trigger" aria-haspopup="menu" aria-expanded="false">${icon('shield-check')}<span>Ask</span></button><div id="permission-menu" class="model-menu" role="menu" hidden></div></div>
        ${button('refresh-cw', '刷新记录', 'refresh')}
      </header>
      <div id="error" role="alert" hidden><span></span>${button('x', '关闭提示', 'dismiss')}</div>
      <div id="connection-notice" role="status" aria-live="polite" hidden></div>
      <section id="keys-view" class="keys-view" hidden aria-labelledby="keys-title"></section>
      <section id="capabilities-view" class="keys-view" hidden aria-labelledby="capabilities-title"></section>
      <section id="permissions-view" class="keys-view" hidden aria-labelledby="permissions-title"></section>
      <div id="run-bar" role="status" aria-live="polite">
        <div id="run-status"></div>
        <div id="run-details"></div>
        <div id="run-controls"></div>
      </div>
      <div id="reader" tabindex="0" aria-label="对话记录">
        <button id="earlier" class="text-action" data-action="earlier" hidden>加载更早的消息</button>
        <div id="turns"></div>
        <div id="stream" aria-live="off"></div>
        <div id="empty"><span class="empty-mark" aria-hidden="true">${icon('message-square')}</span><h2>新对话</h2></div>
      </div>
      <div class="layout-resizer column-resizer column-resizer-start" data-layout-resizer="column" data-layout-side="start" role="separator" aria-orientation="vertical" aria-label="调整阅读区宽度" tabindex="0"></div>
      <div class="layout-resizer column-resizer column-resizer-end" data-layout-resizer="column" data-layout-side="end" role="separator" aria-orientation="vertical" aria-label="调整阅读区宽度" tabindex="0"></div>
      <div class="latest-wrap"><button id="latest" data-action="latest" hidden>${icon('arrow-down')}最新消息</button></div>
      <footer class="composer">
        <div class="layout-resizer composer-resizer" data-layout-resizer="composer" role="separator" aria-orientation="horizontal" aria-label="调整输入框高度" tabindex="0"></div>
        <div id="memory-panel"></div>
        <div class="input-surface">
          <textarea id="prompt" aria-label="输入任务" placeholder="输入任务..." rows="3"></textarea>
          <div id="command-menu" class="model-menu command-menu" role="listbox" hidden></div>
          <div id="draft-preview" class="markdown" hidden aria-label="草稿预览"></div>
          <div class="input-toolbar"><div class="segmented" role="group" aria-label="输入模式">
            ${button('pencil-line', '编辑草稿', 'edit', 'aria-pressed="true"')}
            ${button('eye', '预览草稿', 'preview', 'aria-pressed="false"')}
          </div><button id="capability-trigger" type="button" class="icon capability-trigger" title="本对话能力" aria-label="本对话能力" aria-haspopup="menu" aria-expanded="false">${icon('blocks')}<span id="capability-count"></span></button><div id="capability-menu" class="model-menu capability-menu" role="menu" hidden></div><span id="input-state"></span><button id="send" class="send" data-action="send" title="发送" aria-label="发送">${icon('arrow-up')}<span id="send-label" class="send-label">发送</span></button></div>
        </div>
      </footer>
    </main>
  </div>
  <dialog id="ending" aria-labelledby="ending-title">
    <header><h2 id="ending-title">结束当前任务？</h2>${button('x', '取消', 'close-end')}</header>
    <div id="ending-content"></div>
    <label id="end-ack-line" class="end-ack" hidden><input id="end-ack" type="checkbox">我已核对记录</label>
    <footer><button class="text-action" data-action="close-end">取消</button><button class="danger" data-action="end">确认结束</button></footer>
  </dialog>
  <dialog id="limits" aria-labelledby="limits-title">
    <header><h2 id="limits-title">任务限额</h2>${button('x', '关闭限额', 'close-limits')}</header>
    <label>最大调用步数<input id="limit-steps" type="number" min="1" max="100" step="1"></label>
    <label>最大运行秒数<input id="limit-seconds" type="number" min="1" max="3600" step="1"></label>
    <footer><button class="primary" data-action="save-limits">${icon('check')}保存限额</button></footer>
  </dialog>
  <dialog id="permission-dialog" aria-labelledby="permission-dialog-title">
    <header><h2 id="permission-dialog-title">启用 Full access？</h2>${button('x', '取消', 'close-permission-dialog')}</header>
    <p>允许 FreeAgent 在当前 Workspace 和已启用工具范围内自主执行文件操作、命令和外部调用吗？</p>
    <p class="subtle-note">你仍可以随时停止运行，执行结果会被记录。</p>
    <footer><button class="text-action" data-action="close-permission-dialog">取消</button><button class="primary" data-action="confirm-permission-dialog">${icon('shield-check')}确认启用</button></footer>
  </dialog>
  <dialog id="rename-dialog" aria-labelledby="rename-title">
    <header><h2 id="rename-title">重命名会话</h2>${button('x', '取消', 'close-rename')}</header>
    <label>会话名称<input id="rename-input" type="text" maxlength="80" autocomplete="off"></label>
    <p id="rename-error" class="warning" hidden></p>
    <footer><button class="text-action" data-action="close-rename">取消</button><button class="primary" data-action="save-rename">${icon('check')}保存</button></footer>
  </dialog>
  <dialog id="workflow-input-dialog" aria-labelledby="workflow-input-title">
    <header><h2 id="workflow-input-title">运行 Workflow</h2>${button('x', '取消', 'close-workflow-input')}</header>
    <p id="workflow-input-description" class="subtle-note"></p>
    <label>输入（JSON）
      <textarea id="workflow-input" rows="8" spellcheck="false" autocapitalize="off"></textarea>
    </label>
    <p id="workflow-input-error" class="warning" hidden></p>
    <footer><button class="text-action" data-action="close-workflow-input">取消</button><button class="primary" data-action="run-workflow-input">${icon('play')}运行</button></footer>
  </dialog>`;

const layoutBinding = bindLayoutResizers({ storage });

function reportDesktopBusy(value, authoritative = false) {
  const next = Boolean(value);
  if (next || authoritative) desktopBusy = next;
  setDesktopBusy(desktopBusy);
}

installDesktopBridge({
  getBusy: () => desktopBusy,
  getWorkspace: () => state.env?.workspace || '',
  onChanged: () => {
    state.mutation = true;
    state.workspaceChanging = true;
    error('正在更换工作区。页面将重新加载，请稍候。');
    controls();
  },
  onError: (message) => error(message),
  getChatContext: (id) => state.chats.find((chat) => chat.id === id),
  onChatPin: (id, pinned) => togglePin(id, pinned),
});

function decorate(root = document) {
  createIcons({ icons, root, attrs: { 'aria-hidden': 'true', 'stroke-width': 1.7 } });
}
function measured(name, started) {
  // Keep recent diagnostic samples without retaining an unbounded timeline.
  if (performance.getEntriesByName(name, 'measure').length >= 256) performance.clearMeasures(name);
  performance.measure(name, { start: started, end: performance.now() });
}
function error(text) {
	const message = String(text || '');
	const revision = ++errorRevision;
	clearTimeout(errorTimer);
	errorTimer = 0;
	$('#error span').textContent = message;
	$('#error').hidden = !message;
	if (!message) return;
	errorTimer = setTimeout(() => {
		if (revision !== errorRevision) return;
		errorTimer = 0;
		$('#error span').textContent = '';
		$('#error').hidden = true;
	}, 2000);
}
function elapsedText(milliseconds) {
  const seconds = Math.max(0, Math.floor(Number(milliseconds || 0) / 1000));
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes} 分 ${String(seconds % 60).padStart(2, '0')} 秒`;
}
function updateConnectionNotice() {
  const notice = $('#connection-notice');
  if (state.online || !state.offlineSince) {
    notice.hidden = true;
    notice.textContent = '';
    return;
  }
  notice.hidden = false;
  notice.innerHTML = `${icon('wifi-off')}<span>${Date.now() - state.offlineSince >= 30000
    ? '无法连接到 FreeAgent，请关闭后重新打开'
    : '连接已断开，正在重连'}</span>`;
  decorate(notice);
}
function connection(online) {
  state.online = online;
  if (online) {
    state.connectionEver = true;
    state.offlineSince = 0;
    clearTimeout(offlineTimer);
    offlineTimer = 0;
  } else if (!state.offlineSince) {
    state.offlineSince = Date.now();
    clearTimeout(offlineTimer);
    offlineTimer = setTimeout(() => {
      offlineTimer = 0;
      updateConnectionNotice();
      controls();
    }, 30000);
  }
  $('#connection').textContent = online ? '已连接' : '连接中断';
  $('#connection').classList.toggle('offline', !online);
  updateConnectionNotice();
  controls();
}
async function api(path, body, method = '') {
  const response = await fetch(`/api/${path}`, { method: method || (body === undefined ? 'GET' : 'POST'),
    headers: { 'X-FreeAgent-Key': key, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body), cache: 'no-store' });
  const data = await response.json();
  if (!response.ok) {
    const failure = new Error(data.error || '请求未完成。');
    failure.code = data.code;
    failure.data = data;
    failure.status = response.status;
    failure.definitive = true;
    throw failure;
  }
  return data;
}
const chatAPI = (action, body) => api(`chats/${encodeURIComponent(state.chat)}${action ? `/${action}` : ''}`, body);
function selectedModelID() {
  return state.pendingModel || (state.chat ? (state.view?.model_id || state.env?.default_model || '') :
    (state.env?.default_model || ''));
}
const PERMISSION_LABELS = {
  ask: 'Ask',
  workspace: 'Workspace access',
  full: 'Full access',
};
function selectedPermissionMode() {
  return permissionMode(state.view, state.pendingPermission);
}
function selectedPermissionSource() {
  return state.pendingPermission ? 'turn' : state.view?.permission?.source || 'default';
}
function selectedCapabilities() {
  return state.pendingCapabilities || state.view?.capabilities || {
    skills: (state.env?.capabilities?.skills || []).filter((item) => item.enabled).map((item) => item.id),
    mcp_servers: (state.env?.capabilities?.mcp_servers || []).filter((item) => item.enabled).map((item) => item.id),
    extensions: (state.env?.capabilities?.extensions || []).filter((item) => item.enabled).map((item) => item.id),
  };
}
function capabilitySourceLabel() {
  return state.pendingCapabilities ? '下一轮' : state.view ? '对话默认' : '全局默认';
}
function capabilityEntries() {
  const catalog = state.env?.capabilities || {};
  const selected = selectedCapabilities();
  return [
    ['skills', 'Skills', 'scroll-text', catalog.skills || [], selected.skills || []],
    ['mcp_servers', 'MCP', 'plug-zap', catalog.mcp_servers || [], selected.mcp_servers || []],
    ['extensions', '扩展', 'puzzle', catalog.extensions || [], selected.extensions || []],
  ];
}
function renderCapabilityMenu() {
  const trigger = $('#capability-trigger'), menu = $('#capability-menu');
  if (!trigger || !menu) return;
  const selected = selectedCapabilities();
  const count = [...(selected.skills || []), ...(selected.mcp_servers || []), ...(selected.extensions || [])].length;
  $('#capability-count').textContent = count ? String(count) : '';
  trigger.setAttribute('aria-expanded', String(state.capabilityMenuOpen));
  trigger.title = `本对话能力：${count} 项（${capabilitySourceLabel()}）`;
  menu.innerHTML = capabilityEntries().map(([group, label, glyph, items, values]) =>
    `<div class="capability-group"><strong>${icon(glyph)}${label}</strong>${items.length ? items.map((item) => {
      const checked = values.includes(item.id);
      return `<button type="button" role="menuitemcheckbox" class="model-option" data-capability-group="${group}" data-capability-id="${esc(item.id)}" aria-checked="${checked}" ${item.enabled ? '' : 'disabled'}>${checked ? icon('check') : '<span class="menu-check"></span>'}<span>${esc(item.id)}</span></button>`;
    }).join('') : '<small>暂无已启用能力</small>'}</div>`
  ).join('');
  menu.hidden = !state.capabilityMenuOpen;
  decorate(trigger);
  decorate(menu);
}
function visibleCommands() {
  const query = commandQuery($('#prompt')?.value);
  return query === null ? [] : filterCommands(state.commandCatalog, query);
}
function renderCommandMenu() {
  const menu = $('#command-menu');
  if (!menu) return;
  const query = commandQuery($('#prompt')?.value);
  const entries = query === null ? [] : filterCommands(state.commandCatalog, query);
  const visible = state.commandMenuOpen && entries.length && !state.busy && !state.elsewhere;
  if (!visible) {
    menu.hidden = true;
    menu.replaceChildren();
    return;
  }
  state.commandIndex = Math.max(0, Math.min(state.commandIndex, entries.length - 1));
  menu.innerHTML = entries.map((entry, index) => `
    <button type="button" role="option" class="model-option command-option ${index === state.commandIndex ? 'selected' : ''}" data-command-index="${index}" aria-selected="${index === state.commandIndex}">
      ${icon(entry.icon)}<span><strong>/${esc(entry.label)}</strong><small>${esc(entry.source)}${entry.description ? ` · ${esc(entry.description)}` : ''}</small></span>
    </button>`).join('');
  menu.hidden = false;
  decorate(menu);
}
function closeCommandMenu() {
  state.commandMenuOpen = false;
  state.commandIndex = -1;
  renderCommandMenu();
}
function updateCommandMenu() {
  const query = commandQuery($('#prompt')?.value);
  if (query === null) {
    closeCommandMenu();
    return;
  }
  const entries = filterCommands(state.commandCatalog, query);
  state.commandMenuOpen = entries.length > 0;
  state.commandIndex = entries.length ? Math.min(Math.max(state.commandIndex, 0), entries.length - 1) : -1;
  renderCommandMenu();
}
function selectedCommand() {
  const entries = visibleCommands();
  return entries[state.commandIndex] || entries[0] || null;
}
function rebuildCommandCatalog() {
  state.commandCatalog = commandEntries({
    skills: state.skills?.skills || [],
    extensions: state.extensions?.extensions || [],
  });
  updateCommandMenu();
}
function openWorkflowInput(entry) {
  state.pendingCommand = entry;
  $('#workflow-input-title').textContent = `运行 /${entry.name}`;
  $('#workflow-input-description').textContent =
    `${entry.source}${entry.description ? `：${entry.description}` : ''}${entry.inputSummary ? `（字段：${entry.inputSummary}）` : ''}`;
  $('#workflow-input').value = commandInputDefaults(entry);
  $('#workflow-input-error').hidden = true;
  $('#workflow-input-dialog').showModal();
  requestAnimationFrame(() => $('#workflow-input').focus());
}
function closeWorkflowInput() {
  state.pendingCommand = null;
  $('#workflow-input-dialog')?.close();
}
async function runWorkflowInput() {
  const entry = state.pendingCommand;
  if (!entry) return;
  let input;
  try {
    input = JSON.parse($('#workflow-input').value || '{}');
    if (!input || Array.isArray(input) || typeof input !== 'object') throw new Error('输入必须是 JSON 对象。');
  } catch (failure) {
    const target = $('#workflow-input-error');
    target.textContent = failure.message || '输入必须是有效 JSON。';
    target.hidden = false;
    return;
  }
  closeWorkflowInput();
  await runWorkflow(entry, input);
}
async function chooseCommand(entry = selectedCommand()) {
  if (!entry) return;
  closeCommandMenu();
  if (entry.kind === 'skill') {
    $('#prompt').value = `/${entry.name} `;
    draft();
    $('#prompt').focus();
    return;
  }
  if (commandNeedsInput(entry)) {
    openWorkflowInput(entry);
    return;
  }
  await runWorkflow(entry, {});
}
async function loadCommandCatalog() {
  const [skills, extensions] = await Promise.all([api('skills'), api('extensions')]);
  state.skills = skills;
  state.extensions = extensions;
  rebuildCommandCatalog();
}
function closeCapabilityMenu() {
  state.capabilityMenuOpen = false;
  renderCapabilityMenu();
}
async function toggleCapability(group, id) {
  const current = selectedCapabilities();
  const values = new Set(current[group] || []);
  values.has(id) ? values.delete(id) : values.add(id);
  const next = { skills: [...(current.skills || [])], mcp_servers: [...(current.mcp_servers || [])], extensions: [...(current.extensions || [])] };
  next[group] = [...values].sort();
  state.pendingCapabilities = next;
  renderCapabilityMenu();
  if (state.chat && !state.busy) {
    await chatAPI('capabilities', next);
    state.pendingCapabilities = null;
    await snapshot();
  }
  controls();
}
function permissionSourceLabel(source) {
  return source === 'turn' ? '新会话待保存' : source === 'conversation' ? '当前会话' : '全局默认';
}
function renderPermissionMenu() {
  const trigger = $('#permission-trigger'), menu = $('#permission-menu');
  if (!trigger || !menu) return;
  const selected = selectedPermissionMode();
  const source = permissionSourceLabel(selectedPermissionSource());
  trigger.innerHTML = `${icon('shield-check')}<span>${esc(PERMISSION_LABELS[selected] || 'Ask')} · ${esc(source)}</span>`;
  trigger.classList.remove('permission-ask', 'permission-workspace', 'permission-full');
  trigger.classList.add(`permission-${selected}`);
  trigger.title = '权限管理';
  trigger.setAttribute('aria-label', '权限管理');
  trigger.setAttribute('aria-expanded', String(state.permissionMenuOpen));
  menu.innerHTML = [
    ['ask', '逐项确认文件、命令和外部调用'],
    ['workspace', '当前 Workspace 内文件操作自动执行'],
    ['full', '文件、命令和已启用工具自动执行'],
  ].map(([id, description]) => `<button type="button" role="menuitemradio" class="model-option permission-${id} ${id === selected ? 'selected' : ''}" data-permission-id="${id}" aria-checked="${id === selected}">${id === selected ? icon('check') : '<span class="menu-check"></span>'}<span><strong>${PERMISSION_LABELS[id]}${id === selected ? ` · ${permissionSourceLabel(selectedPermissionSource())}` : ''}</strong><small>${description}</small></span></button>`).join('') +
    `<button type="button" role="menuitem" class="menu-action permission-settings" data-action="permissions">${icon('settings-2')}<span>打开权限管理</span></button>`;
  menu.hidden = !state.permissionMenuOpen;
  decorate(trigger);
  decorate(menu);
}
function closePermissionMenu(restore = true) {
  state.permissionMenuOpen = false;
  renderPermissionMenu();
  if (restore) $('#permission-trigger')?.focus();
}
async function choosePermission(mode) {
  if (!PERMISSION_LABELS[mode]) return;
  if (mode === 'full') {
    state.permissionDialogMode = mode;
    closePermissionMenu(false);
    $('#permission-dialog')?.showModal();
    return;
  }
  closePermissionMenu(false);
  if (!state.chat) {
    state.permissionConfirmed = false;
    state.pendingPermission = mode;
    renderPermissionMenu();
    controls();
    return;
  }
  await saveConversationPermission(mode, false);
}
function closePermissionDialog() {
  state.permissionDialogMode = '';
  $('#permission-dialog')?.close();
}
async function confirmPermissionDialog() {
  if (state.permissionDialogMode !== 'full') return;
  const mode = state.permissionDialogMode;
  closePermissionDialog();
  if (!state.chat) {
    state.permissionConfirmed = true;
    state.pendingPermission = mode;
    renderPermissionMenu();
    controls();
    return;
  }
  await saveConversationPermission(mode, true);
}
async function saveConversationPermission(mode, confirmed) {
  const previous = state.view?.permission?.mode || 'ask';
  try {
    const result = await api(`conversations/${encodeURIComponent(state.chat)}/permission`, { mode, confirmed }, 'PATCH');
    state.pendingPermission = '';
    state.permissionConfirmed = false;
    await snapshot();
    if (result.permission && state.view) state.view.permission = result.permission;
    renderPermissionMenu();
  } catch (failure) {
    state.pendingPermission = '';
    state.permissionConfirmed = false;
    if (state.view?.permission) state.view.permission.mode = previous;
    renderPermissionMenu();
    throw failure;
  }
}
function renderModelMenu() {
  const trigger = $('#model-trigger'), menu = $('#model-menu');
  if (!trigger || !menu) return;
  const items = modelMenuItems(state.env, state.keys?.profiles || []);
  const selected = selectedModelID();
  const selectedItem = items.find((item) => item.id === selected);
  trigger.innerHTML = `${icon('chevron-down')}<span>${esc(selectedItem?.label || (selected === 'auto' ? 'Auto' : '选择模型'))}</span>`;
  trigger.setAttribute('aria-label', `当前模型：${selectedItem?.label || '选择模型'}`);
  trigger.setAttribute('aria-expanded', String(state.modelMenuOpen));
  menu.innerHTML = items.map((item, index) => item.kind === 'action'
    ? `<button type="button" role="menuitem" class="menu-action" data-model-menu-action="${item.id}" data-index="${index}">${esc(item.label)}</button>`
    : `<button type="button" role="menuitemradio" class="model-option ${item.id === selected ? 'selected' : ''}" data-model-id="${esc(item.id)}" data-index="${index}" aria-checked="${item.id === selected}" ${item.disabled ? 'disabled' : ''}>${item.id === selected ? icon('check') : '<span class="menu-check"></span>'}<span>${esc(item.label)}</span></button>`
  ).join('');
  menu.hidden = !state.modelMenuOpen;
  if (state.modelMenuOpen) {
    const focusable = [...menu.querySelectorAll('[role="menuitem"], [role="menuitemradio"]')];
    focusable[state.modelMenuIndex]?.focus();
  }
  decorate(trigger);
  decorate(menu);
}
function closeModelMenu(restore = true) {
  state.modelMenuOpen = false;
  state.modelMenuIndex = -1;
  renderModelMenu();
  if (restore) $('#model-trigger')?.focus();
}
async function openModelMenu() {
  if ($('#model-trigger').disabled) return;
  await refreshEnvironment().catch(() => {});
  const items = modelMenuItems(state.env, state.keys?.profiles || []);
  state.modelMenuOpen = true;
  state.modelMenuIndex = Math.max(0, nextIndex(items, -1, 1));
  renderModelMenu();
}
async function chooseModel(id) {
  if (id === 'add-key' || id === 'manage-keys') {
    closeModelMenu(false);
    await openKeys();
    return;
  }
  const item = modelMenuItems(state.env, state.keys?.profiles || []).find((entry) => entry.id === id);
  if (!item || item.disabled) return;
  closeModelMenu(false);
  if (!state.chat) {
    state.pendingModel = id;
    renderModelMenu();
    controls();
    return;
  }
  const previous = selectedModelID();
  try {
    await chatAPI('model', { model: id });
    state.pendingModel = '';
    await snapshot();
  } catch (failure) {
    state.pendingModel = previous;
    renderModelMenu();
    error(failure.message || '模型选择未保存。');
  }
}
function renderModelOptions() {
  renderModelMenu();
}
function syncPage() {
  const keys = state.page === 'keys';
  const capabilities = state.page === 'capabilities';
  const permissions = state.page === 'permissions' || state.page === 'settings';
  $('main').classList.toggle('keys-page', keys);
  $('main').classList.toggle('capabilities-page', capabilities);
  $('main').classList.toggle('permissions-page', permissions);
  $('main').classList.toggle('settings-page', permissions);
  $('#keys-view').hidden = !keys;
  $('#capabilities-view').hidden = !capabilities;
  $('#permissions-view').hidden = !permissions;
  $('#reader').hidden = keys || capabilities || permissions;
  $('.latest-wrap').hidden = keys || capabilities || permissions;
  $('.composer').hidden = keys || capabilities || permissions;
  $('#run-bar').hidden = keys || capabilities || permissions;
  $('.model-label').hidden = keys || capabilities || permissions;
  $('.permission-label').hidden = keys || capabilities || permissions;
  $('#rename-chat').hidden = !state.chat || keys || capabilities || permissions;
  $('#title').textContent = keys ? 'Key 管理' : capabilities ? '能力管理' : permissions ? '设置' : state.view?.conversation?.title || (state.drafting ? '新对话' : 'FreeAgent');
  if (!keys && !capabilities && !permissions) {
    renderModelMenu();
    renderPermissionMenu();
    renderCapabilityMenu();
  }
  renderMemoryPanel();
}
async function refreshEnvironment() {
  state.env = await api('environment');
  layoutBinding.setEnvironment?.(state.env.environment);
  $('#workspace-path').textContent = state.env.workspace;
  $('#workspace-path').title = state.env.workspace;
  renderModelMenu();
  renderPermissionMenu();
  renderCapabilityMenu();
  renderTurns();
  controls();
}
async function loadPermissions() {
  const query = state.chat ? `?chat_id=${encodeURIComponent(state.chat)}` : '';
  const [settings, permissions] = await Promise.all([api('settings'), api(`permissions${query}`)]);
  state.settings = settings;
  state.permissions = permissions;
  state.permissionRootDraft = [...(settings.file_access?.allowed_roots || permissions.profile?.additional_roots || permissions.computer_roots || [])];
  state.permissionBlockedDraft = [...(settings.file_access?.global_blocked_paths || [])];
  return state.permissions;
}
function permissionPageRoots() {
  return [...(state.permissionRootDraft || state.settings?.file_access?.allowed_roots || state.permissions?.profile?.additional_roots || [])];
}
function permissionPageBlocked() {
  return [...(state.permissionBlockedDraft || state.settings?.file_access?.global_blocked_paths || [])];
}
function permissionPageCommands() {
  return String($('#command-allowlist')?.value || '').split(/\r?\n/).map((item) => item.trim()).filter(Boolean);
}
function renderPermissionsView() {
  const root = $('#permissions-view');
  if (!root) return;
  const data = state.permissions || {};
  const settings = state.settings || {};
  const selection = data.selection || state.view?.permission || { mode: 'ask', computer_files: false, computer_roots: [] };
  const fileAccess = settings.file_access || {};
  const scope = fileAccess.scope || 'workspace';
  const tools = settings.enabled_tools || { file: true, command_run: false, network: false, memory: true };
  const network = settings.network || { enabled: false, allowed_origins: [] };
  const command = settings.command_run || data.commands || { enabled: false, allowed_executables: [], timeout_seconds: 120, max_output_bytes: 32768, inherit_environment: false, execution: 'sandboxed', rules: [], generation: settings.revision || '1' };
  const commandCatalog = data.command_catalog || [];
  const activeNotice = data.active_run_uses_previous_snapshot
    ? '<p class="permission-notice">当前运行沿用创建时的权限和工具快照；本页保存内容只影响下一次新 Run。</p>' : '';
  const currentMode = selection.mode || 'ask';
  const protectedPaths = fileAccess.protected_paths || [];
  const roots = permissionPageRoots();
  const blocked = permissionPageBlocked();
  const rootRows = roots.length ? roots.map((rootPath) => `<div class="permission-root-row"><code title="${esc(rootPath)}">${esc(rootPath)}</code><button class="icon" data-action="remove-permission-root" data-root="${esc(rootPath)}" title="删除授权项" aria-label="删除授权项">${icon('trash-2')}</button></div>`).join('') : '<p class="subtle-note">尚未添加授权目录或文件。</p>';
  const blockedRows = blocked.length ? blocked.map((path) => `<div class="permission-root-row"><code title="${esc(path)}">${esc(path)}</code><button class="icon" data-action="remove-blocked-path" data-path="${esc(path)}" title="删除禁止项" aria-label="删除禁止项">${icon('trash-2')}</button></div>`).join('') : '<p class="subtle-note">尚未添加全局禁止路径。</p>';
  const protectedRows = protectedPaths.length ? protectedPaths.map((path) => `<div class="permission-root-row protected"><span aria-hidden="true">${icon('lock')}</span><code title="${esc(path)}">${esc(path)}</code><span class="subtle-note">系统保护</span></div>`).join('') : '';
  const permissionMode = data.profile?.permission_mode || settings.default_permission_mode || 'ask';
  const commandEnabled = Boolean(tools.command_run && command.enabled);
  const effectiveCommandStatus = commandEnabled && command.execution === 'host_explicit'
    ? (permissionMode === 'full' ? 'ready' : 'permission_denied')
    : command.status;
  const statusLabel = effectiveCommandStatus === 'unsupported' ? '后端不可用' : effectiveCommandStatus === 'permission_denied' ? '当前权限不允许' : effectiveCommandStatus === 'ready' ? '可执行' : '已关闭';
  const sandboxLabel = command.sandbox_supported === false ? '安全沙箱（当前系统不可用）' : '安全沙箱';
  const catalogSandboxLabels = { sandboxed: '沙箱', host_explicit: '宿主机', unsupported: '沙箱不可用', unavailable: '未找到' };
  const commandEnabledLabel = commandEnabled ? '已开启' : '已关闭';
  root.innerHTML = `<div class="keys-header"><div><h2 id="permissions-title">设置</h2><p>全局权限、文件范围和内置工具设置。能力管理保持独立；修改只影响新 Run。</p></div><button class="icon" data-action="close-permissions" title="返回对话" aria-label="返回对话">${icon('arrow-left')}</button></div>
    ${activeNotice}
    <div class="permission-layout">
      <section class="permission-section"><header><div><h3>权限模式</h3><p>默认值用于新会话；当前会话模式单独保存并立即生效于下一次新 Run。</p></div><span class="permission-badge permission-${esc(currentMode)}">${esc(PERMISSION_LABELS[currentMode] || 'Ask')}</span></header>
        <label>全局默认模式<select id="settings-default-mode"><option value="ask" ${settings.default_permission_mode === 'ask' ? 'selected' : ''}>Ask</option><option value="workspace" ${settings.default_permission_mode === 'workspace' ? 'selected' : ''}>Workspace access</option><option value="full" ${settings.default_permission_mode === 'full' ? 'selected' : ''}>Full access</option></select></label>
        ${state.chat ? `<label>当前会话模式<select id="page-permission-mode"><option value="ask" ${currentMode === 'ask' ? 'selected' : ''}>Ask</option><option value="workspace" ${currentMode === 'workspace' ? 'selected' : ''}>Workspace access</option><option value="full" ${currentMode === 'full' ? 'selected' : ''}>Full access</option></select></label><div class="permission-actions"><button class="primary" data-action="save-permission-page">${icon('save')}保存当前会话模式</button></div>` : '<p class="subtle-note">当前是新会话草稿，顶部权限按钮会在首次发送时保存。</p>'}
        <div class="permission-actions"><button class="primary" data-action="save-settings-permission">${icon('save')}保存全局默认</button></div>
      </section>
      <section class="permission-section"><header><div><h3>文件访问范围</h3><p>保护路径始终拒绝；目录授权不会解除数据目录和 Key 存储保护。</p></div><span class="permission-state ${scope !== 'workspace' ? 'enabled' : ''}">${esc(scope)}</span></header>
        <label>全局文件范围<select id="execution-file-scope"><option value="workspace" ${scope === 'workspace' ? 'selected' : ''}>当前工作区</option><option value="workspace_plus_allowed_roots" ${scope === 'workspace_plus_allowed_roots' ? 'selected' : ''}>工作区 + 全局授权目录</option><option value="all_non_protected" ${scope === 'all_non_protected' ? 'selected' : ''}>所有非保护文件</option></select></label>
        <div class="permission-root-list" id="permission-root-list">${rootRows}</div>
        <div class="permission-root-add"><button class="text-action" data-action="add-permission-folder">${icon('folder-plus')}添加文件夹</button><button class="text-action" data-action="add-permission-file">${icon('file-plus')}添加文件</button></div>
        <h4>全局禁止访问</h4><p class="subtle-note">所有会话和新 Run 遵守此列表。父目录授权不会覆盖这里的子路径。</p>
        <div class="permission-root-list" id="blocked-path-list">${protectedRows}${blockedRows}</div>
        <div class="permission-root-add"><button class="text-action" data-action="add-blocked-folder">${icon('folder-plus')}添加禁止文件夹</button><button class="text-action" data-action="add-blocked-file">${icon('file-plus')}添加禁止文件</button></div>
        <div class="permission-actions"><button class="primary" data-action="save-computer-files">${icon('save')}保存电脑文件设置</button></div>
      </section>
      <section class="permission-section"><header><div><h3>内置工具</h3><p>关闭的工具不会进入新 Run；正在运行的任务继续使用自己的快照。工具开关和执行权限是两件事。</p></div></header>
        <label class="check-line"><input id="tool-file" type="checkbox" ${tools.file ? 'checked' : ''}>文件工具</label>
        <label class="check-line"><input id="tool-command" type="checkbox" ${tools.command_run ? 'checked' : ''}>command_run <span class="permission-state ${tools.command_run ? 'enabled' : ''}">${esc(commandEnabledLabel)}</span><span class="subtle-note">当前：${esc(statusLabel)}</span></label>
        <label class="check-line"><input id="tool-network" type="checkbox" ${tools.network ? 'checked' : ''}>网络工具 <span class="permission-state ${tools.network ? 'enabled' : ''}">${tools.network ? '已开启' : '已关闭'}</span></label>
        <label>允许的网络来源（每行一个完整 http(s)://host[:port] 地址）<textarea id="network-origins" rows="3" spellcheck="false" placeholder="https://docs.example.com\nhttp://127.0.0.1:8080">${esc((network.allowed_origins || []).join('\n'))}</textarea></label>
        <p class="subtle-note">网络工具必须至少有一个允许来源；来源只允许主机，不包含路径、查询参数、凭据或片段。</p>
        <label class="check-line"><input id="tool-memory" type="checkbox" ${tools.memory ? 'checked' : ''}>记忆工具</label>
        <div class="permission-actions"><button class="primary" data-action="save-settings-tools">${icon('save')}保存工具设置</button></div>
      </section>
      <section class="permission-section"><header><div><h3>command_run（高级）</h3><p>工具开关：${esc(commandEnabledLabel)}；当前状态：${esc(statusLabel)}。沙箱不可用时不会回退宿主机；宿主机模式可以先保存，但只有当前 Run 使用 Full access 时才会执行。</p></div><span class="permission-state ${commandEnabled ? 'enabled' : ''}">${commandEnabled ? '已开启' : '已关闭'}</span></header>
        <label>执行后端<select id="command-execution"><option value="sandboxed" ${command.execution !== 'host_explicit' ? 'selected' : ''}>${esc(sandboxLabel)}</option><option value="host_explicit" ${command.execution === 'host_explicit' ? 'selected' : ''}>高级宿主机</option></select></label>
        <details><summary>高级命令规则</summary><label>可执行文件白名单（每行一个绝对路径）<textarea id="command-allowlist" rows="3" spellcheck="false"></textarea></label></details>
        <div class="permission-grid"><label>超时（秒）<input id="command-timeout" type="number" min="1" max="600" value="${Number(command.timeout_seconds) || 120}"></label><label>输出上限（字节）<input id="command-output" type="number" min="1024" max="1048576" value="${Number(command.max_output_bytes) || 32768}"></label></div>
        <label class="check-line"><input id="command-inherit" type="checkbox" ${command.inherit_environment ? 'checked' : ''}>请求继承环境变量（运行时仍使用敏感信息过滤后的最小集合）</label>
        <label class="check-line"><input id="command-git-write" type="checkbox" ${command.git_write ? 'checked' : ''}>允许沙箱命令写入 .git（hooks 和 config 始终只读）</label>
        <div class="permission-catalog"><strong>检测到的程序</strong>${commandCatalog.length ? commandCatalog.map((entry) => `<div class="permission-catalog-row"><span>${esc(entry.program)}</span><code>${esc(entry.path || '未找到')}</code><span class="subtle-note">${esc(entry.risk)} · ${esc(catalogSandboxLabels[entry.sandbox] || entry.sandbox)}</span></div>`).join('') : '<p class="subtle-note">尚未发现可用程序。</p>'}</div>
        <p class="subtle-note">当前 generation：<code>${esc(command.generation || data.generation || '1')}</code></p>
        <div class="permission-actions"><button class="primary" data-action="save-command-policy">${icon('save')}保存命令策略</button></div>
      </section>
    </div>`;
  $('#command-allowlist').value = (command.allowed_executables || []).join('\n');
  decorate(root);
  syncPage();
}
async function openPermissions() {
  state.page = 'settings';
  state.permissionMenuOpen = false;
  closeModelMenu(false);
  syncPage();
  try {
    await loadPermissions();
    renderPermissionsView();
  } catch (failure) {
    renderPermissionsView();
    error(failure.message || '权限配置读取失败。');
  }
}
function closePermissions() {
  state.page = 'chat';
  state.permissions = null;
  state.settings = null;
  state.permissionRootDraft = null;
  state.permissionBlockedDraft = null;
  syncPage();
}
async function openSettings() { return openPermissions(); }
async function saveCommandPolicy() {
  const command = state.settings?.command_run || state.permissions?.commands || {};
  const result = await api('settings/command-run', {
    enabled: Boolean($('#tool-command')?.checked),
    allowed_executables: permissionPageCommands(),
    timeout_seconds: Number($('#command-timeout')?.value),
    max_output_bytes: Number($('#command-output')?.value),
    inherit_environment: Boolean($('#command-inherit')?.checked),
    git_write: Boolean($('#command-git-write')?.checked),
    execution: $('#command-execution')?.value || 'sandboxed',
    expected_revision: state.settings?.revision || command.generation || '',
  }, 'PATCH');
  state.settings = result;
  await loadPermissions();
  renderPermissionsView();
}
async function saveComputerFiles() {
  const roots = permissionPageRoots();
  const scope = $('#execution-file-scope')?.value || 'workspace';
  const enabled = scope !== 'workspace';
  if (enabled || roots.join('\n') !== (state.settings?.file_access?.allowed_roots || []).join('\n') || permissionPageBlocked().length) {
    if (!window.confirm(enabled ? '确认保存新的文件范围和授权项？' : '确认修改全局文件访问设置？')) return;
  }
  const result = await api('settings/file-access', { scope: enabled ? scope : 'workspace', allowed_roots: roots, global_blocked_paths: permissionPageBlocked(), expected_revision: state.settings?.revision || '' }, 'PATCH');
  state.settings = result;
  await loadPermissions();
  renderPermissionsView();
}
async function saveSettingsPermission() {
  const mode = $('#settings-default-mode')?.value || 'ask';
  if (mode === 'full' && state.settings?.default_permission_mode !== 'full' && !window.confirm('确认将新会话默认权限设为 Full access？')) return;
  state.settings = await api('settings/permissions', { default_permission_mode: mode, expected_revision: state.settings?.revision || '' }, 'PATCH');
  await loadPermissions();
  renderPermissionsView();
}
async function saveSettingsTools() {
  const origins = String($('#network-origins')?.value || '').split(/\r?\n/).map((item) => item.trim()).filter(Boolean);
  state.settings = await api('settings/tools', {
    file: Boolean($('#tool-file')?.checked), command_run: Boolean($('#tool-command')?.checked),
    network: Boolean($('#tool-network')?.checked), memory: Boolean($('#tool-memory')?.checked),
    network_allowed_origins: origins,
    expected_revision: state.settings?.revision || '',
  }, 'PATCH');
  await loadPermissions();
  renderPermissionsView();
}
function updatePermissionRootList() {
  const list = $('#permission-root-list');
  if (!list) return;
  const roots = permissionPageRoots();
  list.innerHTML = roots.length ? roots.map((rootPath) => `<div class="permission-root-row"><code title="${esc(rootPath)}">${esc(rootPath)}</code><button class="icon" data-action="remove-permission-root" data-root="${esc(rootPath)}" title="删除授权项" aria-label="删除授权项">${icon('trash-2')}</button></div>`).join('') : '<p class="subtle-note">尚未添加授权目录或文件。</p>';
  decorate(list);
}
function updateBlockedPathList() {
  const list = $('#blocked-path-list');
  if (!list) return;
  const protectedRows = (state.settings?.file_access?.protected_paths || []).map((path) => `<div class="permission-root-row protected"><span aria-hidden="true">${icon('lock')}</span><code>${esc(path)}</code><span class="subtle-note">系统保护</span></div>`).join('');
  const rows = permissionPageBlocked().map((path) => `<div class="permission-root-row"><code>${esc(path)}</code><button class="icon" data-action="remove-blocked-path" data-path="${esc(path)}" title="删除禁止项" aria-label="删除禁止项">${icon('trash-2')}</button></div>`).join('');
  list.innerHTML = protectedRows + (rows || '<p class="subtle-note">尚未添加全局禁止路径。</p>');
  decorate(list);
}
async function addPermissionRoot(kind = 'folder') {
  const value = kind === 'file' ? await selectDesktopFile('') : await selectDesktopFolder('');
  if (!value) return;
  if (!window.confirm(`确认添加授权项“${value}”？`)) return;
  const roots = permissionPageRoots();
  if (!roots.some((item) => item.toLocaleLowerCase() === value.toLocaleLowerCase())) {
    state.permissionRootDraft = [...roots, value];
    updatePermissionRootList();
  }
}
function removePermissionRoot(value) {
  if (!window.confirm(`确认删除授权目录“${value}”？`)) return;
  state.permissionRootDraft = permissionPageRoots().filter((item) => item !== value);
  updatePermissionRootList();
}
async function addBlockedPath(kind = 'folder') {
  const value = kind === 'file' ? await selectDesktopFile('') : await selectDesktopFolder('');
  if (!value) return;
  if (!window.confirm(`确认禁止访问“${value}”？`)) return;
  const paths = permissionPageBlocked();
  if (!paths.some((item) => item.toLocaleLowerCase() === value.toLocaleLowerCase())) {
    state.permissionBlockedDraft = [...paths, value];
    updateBlockedPathList();
  }
}
function removeBlockedPath(value) {
  if (!window.confirm(`确认移除全局禁止项“${value}”？`)) return;
  state.permissionBlockedDraft = permissionPageBlocked().filter((item) => item !== value);
  updateBlockedPathList();
}
async function savePermissionPage() {
  const mode = $('#page-permission-mode')?.value || 'ask';
  if (mode === 'full' && state.view?.permission?.mode !== 'full' && !window.confirm('确认对当前会话启用 Full access？')) return;
  if (!state.chat) {
    state.pendingPermission = mode;
    state.permissionConfirmed = mode === 'full';
    controls();
    return;
  }
  await api(`conversations/${encodeURIComponent(state.chat)}/permission`, { mode, confirmed: mode === 'full' }, 'PATCH');
  state.pendingPermission = '';
  state.permissionConfirmed = false;
  await snapshot();
  await loadPermissions();
  renderPermissionsView();
}
function openRenameDialog() {
  if (!state.chat || !state.view?.conversation) return;
  $('#rename-input').value = state.view.conversation.title || '';
  $('#rename-error').hidden = true;
  $('#rename-dialog').showModal();
  requestAnimationFrame(() => $('#rename-input').focus());
}
function closeRenameDialog() {
  $('#rename-dialog')?.close();
}
async function saveRename() {
  if (!state.chat) return;
  const input = $('#rename-input');
  if (!input.reportValidity()) return;
  const result = await chatAPI('title', { title: input.value });
  if (state.view?.conversation) state.view.conversation.title = result.title;
  const item = state.chats.find((chat) => chat.id === state.chat);
  if (item) item.title = result.title;
  closeRenameDialog();
  renderChatList();
  syncPage();
  setTitleAttention(titleAttention);
}
function selectedKeyProfile() {
  return state.keys?.profiles?.find((profile) => profile.id === state.keyProfile) || null;
}
function renderChatList() {
  const query = state.chatQuery.trim();
  const chats = state.chats.filter((chat) => matchChat(chat, query));
  const list = $('#chats');
  if (!chats.length) {
    list.innerHTML = '<p class="chat-empty">没有匹配的会话</p>';
    return;
  }
  const renderItems = (items) => items.map((chat) => `<div class="chat-row"><button class="chat-item ${chat.id === state.chat ? 'selected' : ''}" data-chat="${esc(chat.id)}" ${chat.id === state.chat ? 'aria-current="page"' : ''}>${icon(chat.id === state.activeChat ? 'loader-circle' : 'message-square')}<span>${esc(chat.title)}</span></button><button class="icon chat-pin ${chat.pinned ? 'pinned' : ''}" data-action="chat-pin" data-chat="${esc(chat.id)}" data-pinned="${Boolean(chat.pinned)}" title="${chat.pinned ? '取消置顶' : '置顶会话'}" aria-label="${chat.pinned ? '取消置顶' : '置顶会话'}">${icon('pin')}</button></div>`).join('');
  const groups = query ? [{ label: '', items: prioritizePinned(chats) }] : groupChats(chats);
  list.innerHTML = groups.length
    ? groups.map((group) => `<div class="chat-group">${group.label ? `<h3>${esc(group.label)}</h3>` : ''}${renderItems(group.items)}</div>`).join('')
    : renderItems(chats);
  if (!query && state.chats.length === 200) list.insertAdjacentHTML('beforeend', '<p class="chat-cap">只显示最近 200 个会话</p>');
  decorate(list);
}
function renderMemoryPanel() {
  const root = $('#memory-panel');
  if (!root) return;
  const memory = state.memory;
  const count = (memory?.entries?.length || 0) + (memory?.knowledge?.length || 0) + (memory?.task_private ? 1 : 0);
  if (!memory || !count) {
    root.replaceChildren();
    return;
  }
  const entries = (memory.entries || []).map((entry) => `<li><span><strong>${esc(entry.title || entry.entry_id)}</strong><small>${esc(entry.scope?.kind || 'private')} · ${esc(entry.state || 'unavailable')}</small></span><button class="icon" data-memory-entry="${esc(encodeURIComponent(JSON.stringify(entry)))}" title="本会话不再使用" aria-label="本会话不再使用">${icon('eye-off')}</button></li>`).join('');
  const knowledge = (memory.knowledge || []).map((scope) => `<li><span><strong>知识范围</strong><small>${esc(scope.owner)}${scope.project ? ` · ${esc(scope.project)}` : ''}</small></span><button class="icon" data-memory-knowledge="${esc(encodeURIComponent(JSON.stringify(scope)))}" title="本会话不再使用" aria-label="本会话不再使用">${icon('eye-off')}</button></li>`).join('');
  root.innerHTML = `<div class="memory-summary"><button class="text-action" data-action="memory-toggle" aria-expanded="${state.memoryOpen}">${icon('database')}记忆 ${count} 项${state.memoryOpen ? ' · 收起' : ''}</button>${state.busy ? '<span>对下一次任务生效</span>' : ''}</div>${state.memoryOpen ? `<div class="memory-list"><ul>${entries}${knowledge}${memory.task_private ? '<li><span><strong>任务私有记忆</strong><small>开启</small></span></li>' : ''}</ul></div>` : ''}`;
  decorate(root);
}
async function loadMemory(chat = state.chat) {
  const epoch = state.epoch;
  if (!chat) { state.memory = null; renderMemoryPanel(); return; }
  state.memoryLoading = true;
  try {
    const value = await api(`chats/${encodeURIComponent(chat)}/memory`);
    if (epoch === state.epoch && chat === state.chat) {
      state.memory = value;
      renderMemoryPanel();
    }
  } finally {
    if (epoch === state.epoch) state.memoryLoading = false;
  }
}
function keyForm() {
  const profile = selectedKeyProfile();
  const value = keyFormValue(profile);
  const root = $('#keys-view');
  const none = root.querySelector('#key-none');
  return {
    id: profile?.id || '',
    name: root.querySelector('#key-name')?.value ?? value.name,
    base_url: root.querySelector('#key-base-url')?.value ?? value.base_url,
    protocol: root.querySelector('#key-protocol')?.value ?? value.protocol,
    note: root.querySelector('#key-note')?.value ?? value.note,
    source: profile?.source === 'env' ? '' : (none?.checked ? 'none' : 'saved'),
    key: root.querySelector('#key-secret')?.value || '',
  };
}
function keyStatusHTML(profile) {
  const status = keyStatus(profile);
  return `<span class="key-status ${esc(status.kind)}">${icon(status.icon)}${esc(status.label)}</span>`;
}
function keyModelRows(profile) {
  const probe = profile ? state.keyModels.get(profile.id) : state.keyProbe;
  if (!profile && !probe) return '<p class="key-hint">保存配置后，可以测试连接并获取模型。</p>';
  const enabled = profile ? modelsForKey(state.env, profile.id) : [];
  const query = state.keyModelQuery.trim().toLocaleLowerCase();
  const fetched = (probe?.models || []).filter((model) => !query || model.toLocaleLowerCase().includes(query));
  const enabledNames = new Set(enabled.map((model) => model.model));
  const rows = enabled.map((model) => `<li class="key-model-row ${model.missing ? 'missing' : ''}">
    <span><strong>${esc(model.model || model.id)}</strong><small>${model.missing ? 'Key 已删除' : `本地模型 ID：${esc(model.id)}`}</small></span>
    <span class="key-model-actions">${state.env.default_model === model.id ? '<span class="default-mark">默认</span>' : `<button class="text-action" data-action="key-default" data-model-id="${esc(model.id)}">设为默认</button>`}
    <button class="icon" data-action="key-disable" data-model-id="${esc(model.id)}" title="停用模型" aria-label="停用模型">${icon('trash-2')}</button></span>
  </li>`).join('');
  const available = fetched.filter((model) => !enabledNames.has(model));
  const availableRows = available.map((model) => `<li class="key-model-row">
    <span><strong>${esc(model)}</strong><small>服务商模型</small></span>
    <button class="text-action" data-action="key-enable" data-model="${esc(model)}" ${profile ? '' : 'disabled'}>添加</button>
  </li>`).join('');
  const addAll = available.length && profile ? `<button class="text-action" data-action="key-enable-all">自动填充 ${available.length} 个模型</button>` : '';
  const manual = `<div class="manual-model"><label>手动填写模型<input id="manual-model" type="text" maxlength="256" value="${esc(state.keyManualModel)}" placeholder="服务商未提供模型列表"></label><button class="text-action" data-action="key-enable-manual" ${profile ? '' : 'disabled'}>添加</button></div>${addAll}`;
  const search = probe?.models?.length ? `<label class="model-search">筛选模型<input id="key-model-search" type="search" value="${esc(state.keyModelQuery)}" placeholder="搜索型号"></label>` : '';
  return `${search}<ul class="key-model-list">${rows || '<li class="key-hint">此配置还没有启用模型。</li>'}${availableRows}</ul>${manual}`;
}
function renderKeysView() {
  const root = $('#keys-view');
  const profiles = state.keys?.profiles || [];
  const profile = selectedKeyProfile();
  // Refreshes on window focus rebuild this view; keep unsaved input and caret.
  const active = root.contains(document.activeElement) ? document.activeElement : null;
  const focusID = active?.id || '';
  const caret = typeof active?.selectionStart === 'number' ? [active.selectionStart, active.selectionEnd] : null;
  const value = keyFormValue(profile, state.keyDrafts.get(profile?.id || ''));
  const protocolLocked = profile && modelsForKey(state.env, profile.id).length > 0;
  root.innerHTML = `<div class="keys-header"><div><h2 id="keys-title">Key 管理</h2><p>Key 只保存在本机；模型配置引用 Key，不保存 Key 原文。</p></div>
    <button class="icon" data-action="key-new" title="新增 Key" aria-label="新增 Key">${icon('plus')}</button></div>
    <div class="keys-layout"><aside class="key-list" aria-label="Key 配置">
      ${profiles.length ? profiles.map((item) => `<button class="key-item ${item.id === state.keyProfile ? 'selected' : ''}" data-key-id="${esc(item.id)}"><strong>${esc(item.name)}</strong><span>${esc(keyHost(item.base_url))}</span>${keyStatusHTML(item)}</button>`).join('') : '<p class="key-empty">还没有 Key 配置。</p>'}
    </aside><div class="key-editor">
      ${profile ? `<div class="key-editor-heading"><div><h3>${esc(profile.name)}</h3><span class="key-id">${esc(profile.id)}</span></div>
        <button class="icon" data-action="key-delete" title="删除 Key" aria-label="删除 Key">${icon('trash-2')}</button></div>` : '<h3>新增 Key</h3>'}
      <div class="key-preset">${!profile ? `<label>预设<select id="key-preset"><option value="">选择预设...</option>${(state.keys?.presets || []).map((preset) => `<option value="${esc(preset.id)}">${esc(preset.name)}</option>`).join('')}</select></label>` : ''}</div>
      <form id="key-form" class="key-form">
        <label>名称<input id="key-name" required maxlength="64" value="${esc(value.name)}"></label>
        <label>协议<select id="key-protocol" ${protocolLocked ? 'disabled' : ''}><option value="openai-compatible" ${value.protocol === 'openai-compatible' ? 'selected' : ''}>OpenAI 兼容</option><option value="anthropic" ${value.protocol === 'anthropic' ? 'selected' : ''}>Anthropic Messages</option></select></label>
        <label>地址<input id="key-base-url" required value="${esc(value.base_url)}" placeholder="https://api.example.com/v1"></label>
        ${profile?.source === 'env' ? `<div class="key-source"><span>来源</span><strong>环境变量：${esc(profile.env_name || '')}</strong></div>` : `<label class="key-secret-label">API Key<input id="key-secret" type="password" autocomplete="new-password" placeholder="${profile?.hint ? `已保存 ****${esc(profile.hint)}，留空保持不变` : profile ? '已保存，留空保持不变' : '输入 API Key'}"></label>
          <label class="check-line"><input id="key-none" type="checkbox" ${value.source === 'none' ? 'checked' : ''}>无需 Key（仅回环地址）</label>`}
        <label>备注<textarea id="key-note" maxlength="200" rows="2">${esc(value.note)}</textarea></label>
        <div class="key-actions"><button class="icon" type="button" data-action="key-test" title="测试并获取模型" aria-label="测试并获取模型">${icon('plug-zap')}</button>
          <button class="primary" type="button" data-action="key-save">${icon('save')}保存</button></div>
      </form>
      ${state.keyNotice ? `<p class="key-notice" role="status">${esc(state.keyNotice)}</p>` : ''}
      <section class="key-model-section"><header><h3>模型</h3><span class="key-hint">${profile && state.keyModels.get(profile.id) ? `最近获取 ${(state.keyModels.get(profile.id).models || []).length} 个` : ''}</span></header>${keyModelRows(profile)}</section>
    </div></div>`;
  decorate(root);
  const secret = root.querySelector('#key-secret');
  if (secret && value.key) secret.value = value.key;
  const focused = focusID ? root.querySelector('#' + CSS.escape(focusID)) : null;
  if (focused) {
    focused.focus({ preventScroll: true });
    if (caret) try { focused.setSelectionRange(...caret); } catch { /* not a text field */ }
  }
}
function rememberKeyDraft() {
  if (!$('#key-form')) return;
  const profile = selectedKeyProfile();
  const id = profile?.id || '';
  const form = keyForm();
  if (keyDraftChanged(profile, form)) state.keyDrafts.set(id, form);
  else state.keyDrafts.delete(id);
}
async function loadKeys(select = state.keyProfile) {
  state.keys = await api('keys');
  state.keyProfile = state.keys.profiles.some((profile) => profile.id === select) ? select : state.keys.profiles[0]?.id || '';
  renderModelOptions();
  renderKeysView();
}
async function openKeys() {
  if (hasView()) draft();
  state.page = 'keys';
  state.keyProfile = state.keyProfile || '';
  syncPage();
  await loadKeys();
}
function mcpFormValue() {
  const root = $('#capabilities-view');
  const lines = (value) => String(value || '').split(/\r?\n/).map((item) => item.trim()).filter(Boolean);
  let envFrom = {};
  const raw = root.querySelector('#mcp-env-from')?.value.trim() || '';
  if (raw) {
    try { envFrom = JSON.parse(raw); } catch { throw new Error('envFrom 必须是 JSON 对象。'); }
  }
  if (!envFrom || Array.isArray(envFrom) || typeof envFrom !== 'object') {
    throw new Error('envFrom 必须是 JSON 对象。');
  }
  return {
    enabled: root.querySelector('#mcp-enabled')?.checked ?? false,
    command: root.querySelector('#mcp-command')?.value.trim() || '',
    args: lines(root.querySelector('#mcp-args')?.value),
    cwd: root.querySelector('#mcp-cwd')?.value.trim() || '',
    envFrom,
    hiddenTools: lines(root.querySelector('#mcp-hidden')?.value),
    trustReadOnlyHint: root.querySelector('#mcp-readonly')?.checked ?? true,
  };
}
function renderCapabilitiesView() {
  const root = $('#capabilities-view');
  if (!root) return;
  if (state.capabilityTab === 'skills') {
    const skills = state.skills?.skills || [];
    const selected = skills.find((item) => item.name === state.skillName);
    const detail = state.skillDetail?.name === state.skillName ? state.skillDetail : selected;
    const list = skills.length ? skills.map((item) => `
      <button class="key-item ${item.name === state.skillName ? 'selected' : ''}" data-skill-id="${esc(item.name)}">
        <strong>${esc(item.name)}</strong>
        <span>${esc(item.source === 'workspace' ? '工作区级' : '用户级')} · ${esc(item.trigger === 'manual' ? '手动' : '自动')}</span>
        <span class="key-status ${item.enabled ? 'available' : item.available ? 'plaintext' : 'reentry'}">${icon(item.enabled ? 'toggle-right' : 'toggle-left')}${esc(item.enabled ? '已启用' : item.available ? '已关闭' : '未信任')}</span>
      </button>`).join('') : '<p class="key-empty">还没有发现 Skill。</p>';
    root.innerHTML = `<div class="keys-header"><div><h2 id="capabilities-title">能力管理</h2><p>Skills、MCP 和扩展共用本轮能力选择。</p></div>
      <div class="capability-page-actions"><button class="icon" data-action="skill-import" title="导入 Skill" aria-label="导入 Skill">${icon('folder-plus')}</button></div></div>
      <div class="capability-tabs" role="tablist"><button role="tab" aria-selected="true" data-capability-tab="skills">${icon('scroll-text')}Skills</button><button role="tab" aria-selected="false" data-capability-tab="mcp">${icon('plug-zap')}MCP</button><button role="tab" aria-selected="false" data-capability-tab="extensions">${icon('puzzle')}扩展</button></div>
      <div class="keys-layout"><aside class="key-list" aria-label="Skills">${list}</aside><div class="key-editor">
        ${detail ? `<div class="key-editor-heading"><div><h3>${esc(detail.name)}</h3><span class="key-id">${esc(detail.source === 'workspace' ? '工作区级' : '用户级')} · ${esc(detail.trigger === 'manual' ? '仅手动触发' : '自动可用')}</span></div>
          ${detail.source === 'user' ? `<button class="icon" data-action="skill-delete" data-skill-id="${esc(detail.name)}" title="删除 Skill" aria-label="删除 Skill">${icon('trash-2')}</button>` : ''}
        </div>
        <div class="skill-meta"><span>${esc(detail.description)}</span>${detail.source === 'workspace' && !detail.workspace_trusted ? `<button class="text-action" data-action="skill-trust" data-skill-trusted="true">信任当前工作区</button>` : detail.source === 'workspace' ? `<button class="text-action" data-action="skill-trust" data-skill-trusted="false">取消信任当前工作区</button>` : ''}</div>
        <div class="skill-control"><span>本地启用</span><button class="icon" data-action="skill-toggle" data-skill-id="${esc(detail.name)}" data-skill-enabled="${detail.enabled ? 'false' : 'true'}" title="${detail.enabled ? '停用 Skill' : '启用 Skill'}" aria-label="${detail.enabled ? '停用 Skill' : '启用 Skill'}">${icon(detail.enabled ? 'toggle-right' : 'toggle-left')}</button><span class="key-hint">${detail.enabled ? '已启用' : detail.available ? '已关闭' : '需先信任工作区'}</span></div>
        <section class="skill-preview"><header><h3>SKILL.md</h3><span class="key-hint">${detail.bytes} 字节 · ${detail.files?.length || 0} 个附件</span></header><div class="markdown">${renderMarkdown(detail.body || '')}</div></section>
        ${detail.files?.length ? `<section class="key-model-section"><header><h3>附件</h3></header><ul class="key-model-list">${detail.files.map((file) => `<li class="key-model-row"><span><strong>${esc(file.path)}</strong><small>${file.bytes} 字节</small></span></li>`).join('')}</ul></section>` : ''}
        ` : `<div class="capability-empty"><span>${icon('scroll-text')}</span><h3>选择一个 Skill</h3><p>从左侧选择 Skill 查看正文和状态。</p></div>`}
      </div></div>`;
    decorate(root);
    return;
  }
  if (state.capabilityTab === 'extensions') {
    const extensions = state.extensions?.extensions || [];
    const selected = extensions.find((item) => item.id === state.extensionId);
    const config = selected;
    const list = extensions.length ? extensions.map((item) => `
      <button class="key-item ${item.id === state.extensionId ? 'selected' : ''}" data-extension-id="${esc(item.id)}">
        <strong>${esc(item.name || item.id)}</strong>
        <span>${esc(item.source === 'workspace' ? '工作区级' : '用户级')} · v${esc(item.version || '')}</span>
        <span class="key-status ${item.state === 'running' ? 'available' : item.available ? 'plaintext' : 'reentry'}">${icon(item.enabled ? 'toggle-right' : 'toggle-left')}${esc(item.enabled ? '已启用' : item.available ? '已关闭' : '未信任')}</span>
      </button>`).join('') : '<p class="key-empty">还没有发现扩展。</p>';
    const settings = JSON.stringify(config?.settings || {}, null, 2);
    const envFrom = JSON.stringify(config?.env_from || {}, null, 2);
    root.innerHTML = `<div class="keys-header"><div><h2 id="capabilities-title">能力管理</h2><p>Skills、MCP 和扩展共用本轮能力选择。</p></div>
      <div class="capability-page-actions"><button class="icon" data-action="extension-import" title="从文件夹加载扩展" aria-label="从文件夹加载扩展">${icon('folder-plus')}</button></div></div>
      <div class="capability-tabs" role="tablist"><button role="tab" aria-selected="false" data-capability-tab="skills">${icon('scroll-text')}Skills</button><button role="tab" aria-selected="false" data-capability-tab="mcp">${icon('plug-zap')}MCP</button><button role="tab" aria-selected="true" data-capability-tab="extensions">${icon('puzzle')}扩展</button></div>
      <div class="keys-layout"><aside class="key-list" aria-label="扩展">${list}</aside><div class="key-editor">
        ${config ? `<div class="key-editor-heading"><div><h3>${esc(config.name || config.id)}</h3><span class="key-id">${esc(config.id)} · ${esc(config.state || 'stopped')}</span></div></div>
          <div class="skill-meta"><span>${esc(config.description || '')}</span>${config.source === 'workspace' && !config.workspace_trusted ? `<button class="text-action" data-action="extension-trust" data-extension-trusted="true">信任当前工作区</button>` : config.source === 'workspace' ? `<button class="text-action" data-action="extension-trust" data-extension-trusted="false">取消信任当前工作区</button>` : ''}</div>
          <div class="skill-control"><span>本地启用</span><button class="icon" data-action="extension-toggle" data-extension-id="${esc(config.id)}" data-extension-enabled="${config.enabled ? 'false' : 'true'}" title="${config.enabled ? '停用扩展' : '启用扩展'}" aria-label="${config.enabled ? '停用扩展' : '启用扩展'}">${icon(config.enabled ? 'toggle-right' : 'toggle-left')}</button><span class="key-hint">${config.enabled ? '已启用' : config.available ? '已关闭' : '需先信任工作区'}</span></div>
          <form id="extension-form" class="key-form">
            <label class="check-line"><input id="extension-required" type="checkbox" ${config.required ? 'checked' : ''} ${config.available ? '' : 'disabled'}>失败时阻止本轮</label>
            <label>设置（JSON）<textarea id="extension-settings" rows="5" ${config.available ? '' : 'disabled'}>${esc(settings)}</textarea></label>
            <label>环境变量映射（JSON）<textarea id="extension-env-from" rows="4" ${config.available ? '' : 'disabled'}>${esc(envFrom)}</textarea></label>
            <div class="key-actions"><button class="icon" type="button" data-action="extension-restart" title="重启扩展" aria-label="重启扩展">${icon('refresh-cw')}</button><button class="icon" type="button" data-action="extension-logs" title="查看日志" aria-label="查看日志">${icon('file-text')}</button><button class="icon" type="button" data-action="extension-clear-data" title="清空扩展数据" aria-label="清空扩展数据">${icon('trash-2')}</button><button class="primary" type="button" data-action="extension-save">${icon('save')}保存</button></div>
          </form><pre id="extension-log" class="mcp-log" hidden></pre>
        ` : `<div class="capability-empty"><span>${icon('puzzle')}</span><h3>选择一个扩展</h3><p>从左侧选择扩展查看状态和配置。</p></div>`}
      </div></div>`;
    decorate(root);
    return;
  }
  const servers = state.mcp?.servers || [];
  const selected = servers.find((item) => item.id === state.mcpServer);
  const config = selected || {
    id: '', enabled: false, command: '', args: [], cwd: '', env_from: {}, hidden_tools: [], trust_read_only_hint: true,
  };
  const list = servers.length ? servers.map((item) =>
    `<button class="key-item ${item.id === state.mcpServer ? 'selected' : ''}" data-mcp-id="${esc(item.id)}"><strong>${esc(item.id)}</strong><span>${esc(item.state || 'stopped')} · ${item.tool_count || 0} 个工具</span><span class="key-status ${item.state === 'running' ? 'available' : item.state === 'error' ? 'failed' : 'plaintext'}">${esc(item.enabled ? (item.state === 'running' ? '运行中' : '已启用') : '已关闭')}</span></button>`
  ).join('') : '<p class="key-empty">还没有 MCP 服务器。</p>';
  const envFromText = JSON.stringify(config.env_from || {}, null, 2);
  root.innerHTML = `<div class="keys-header"><div><h2 id="capabilities-title">能力管理</h2><p>Skills、MCP 和扩展共用本轮能力选择。</p></div>
    <div class="capability-page-actions"><button class="icon" data-action="mcp-new" title="新增 MCP" aria-label="新增 MCP">${icon('plus')}</button><button class="icon" data-action="mcp-restart" title="重启 MCP" aria-label="重启 MCP">${icon('refresh-cw')}</button></div></div>
    <div class="capability-tabs" role="tablist"><button role="tab" aria-selected="${state.capabilityTab === 'skills'}" data-capability-tab="skills">${icon('scroll-text')}Skills</button><button role="tab" aria-selected="${state.capabilityTab === 'mcp'}" data-capability-tab="mcp">${icon('plug-zap')}MCP</button><button role="tab" aria-selected="${state.capabilityTab === 'extensions'}" data-capability-tab="extensions">${icon('puzzle')}扩展</button></div>
    ${state.capabilityTab !== 'mcp' ? `<div class="capability-empty"><span>${icon(state.capabilityTab === 'skills' ? 'scroll-text' : 'puzzle')}</span><h3>${state.capabilityTab === 'skills' ? 'Skills' : '扩展'}</h3><p>${state.capabilityTab === 'skills' ? '已发现的 Skill 可在对话内能力菜单中启用。' : '扩展接口按计划逐阶段接入，默认不会启动外部进程。'}</p></div>` : `<div class="keys-layout"><aside class="key-list" aria-label="MCP 服务器">${list}</aside><div class="key-editor">
      ${config.id ? `<div class="key-editor-heading"><div><h3>${esc(config.id)}</h3><span class="key-id">${esc(config.state || 'stopped')} · ${config.tool_count || 0} 个工具</span></div><button class="icon" data-action="mcp-delete" title="删除 MCP" aria-label="删除 MCP">${icon('trash-2')}</button></div>` : '<h3>新增 MCP</h3>'}
      <form id="mcp-form" class="key-form">
        ${!config.id ? '<label>名称<input id="mcp-id" required maxlength="64" placeholder="例如 tracker"></label>' : ''}
        <label class="check-line"><input id="mcp-enabled" type="checkbox" ${config.enabled ? 'checked' : ''}>启用本地 MCP</label>
        <label>命令<input id="mcp-command" required value="${esc(config.command || '')}" placeholder="C:\\\\path\\\\server.exe"></label>
        <label>参数<textarea id="mcp-args" rows="3" placeholder="每行一个参数">${esc((config.args || []).join('\n'))}</textarea></label>
        <label>工作目录<input id="mcp-cwd" value="${esc(config.cwd || '')}"></label>
        <label>envFrom（JSON）<textarea id="mcp-env-from" rows="4" placeholder='{"TOKEN":"key:profile-id"}'>${esc(envFromText)}</textarea></label>
        <label>隐藏工具<textarea id="mcp-hidden" rows="3" placeholder="每行一个工具名">${esc((config.hidden_tools || []).join('\n'))}</textarea></label>
        <label class="check-line"><input id="mcp-readonly" type="checkbox" ${config.trust_read_only_hint ? 'checked' : ''}>采用只读标注</label>
        <div class="key-actions"><button class="icon" type="button" data-action="mcp-test" title="测试连接" aria-label="测试连接">${icon('plug-zap')}</button>${config.id ? `<button class="icon" type="button" data-action="mcp-logs" title="查看日志" aria-label="查看日志">${icon('file-text')}</button>` : ''}<button class="primary" type="button" data-action="mcp-save">${icon('save')}保存</button></div>
      </form>
      ${config.tools?.length ? `<section class="key-model-section"><header><h3>工具</h3></header><ul class="key-model-list">${config.tools.map((tool) => `<li class="key-model-row"><span><strong>${esc(tool.name)}</strong><small>${esc(tool.alias)} · ${esc(tool.effect)}</small></span></li>`).join('')}</ul></section>` : ''}
      <pre id="mcp-log" class="mcp-log" hidden></pre>
    </div></div>`}
  `;

bindLayoutResizers({ storage });
  decorate(root);
}
async function loadMCP(select = state.mcpServer) {
  state.mcp = await api('mcp');
  state.mcpServer = state.mcp.servers.some((server) => server.id === select) ? select : state.mcp.servers[0]?.id || '';
  renderCapabilitiesView();
}
async function loadSkillDetail(name = state.skillName) {
  state.skillName = name || '';
  state.skillDetail = state.skillName ? await api(`skills/${encodeURIComponent(state.skillName)}/view`) : null;
  renderCapabilitiesView();
}
async function loadSkills(select = state.skillName) {
  state.skills = await api('skills');
  state.skillName = state.skills.skills.some((skill) => skill.name === select)
    ? select : state.skills.skills[0]?.name || '';
  rebuildCommandCatalog();
  state.skillDetail = null;
  if (state.skillName) await loadSkillDetail(state.skillName);
  else renderCapabilitiesView();
}
async function loadExtensions(select = state.extensionId) {
  state.extensions = await api('extensions');
  state.extensionId = state.extensions.extensions.some((item) => item.id === select)
    ? select : state.extensions.extensions[0]?.id || '';
  rebuildCommandCatalog();
  renderCapabilitiesView();
}
async function openCapabilities() {
  if (hasView()) draft();
  state.page = 'capabilities';
  state.capabilityTab = 'mcp';
  syncPage();
  await loadMCP();
}
async function importSkill() {
  const path = window.prompt('输入包含 SKILL.md 的本地文件夹绝对路径：', '');
  if (!path?.trim()) return;
  const imported = await api('skills', { path: path.trim() });
  await loadSkills(imported.name);
  error(`Skill“${imported.name}”已导入，当前保持关闭。`);
}
async function toggleSkill(name, enabled) {
  await api(`skills/${encodeURIComponent(name)}/update`, { enabled });
  await loadSkills(name);
}
async function trustSkills(trusted) {
  await api('skills/trust', { trusted });
  await loadSkills(state.skillName);
}
async function importExtension() {
  const path = window.prompt('输入扩展文件夹绝对路径（其中包含 extension.json）：', '');
  if (!path?.trim()) return;
  const imported = await api('extensions', { path: path.trim() });
  await loadExtensions(imported.id);
  error(`扩展“${imported.name || imported.id}”已加载，当前保持关闭。`);
}
async function toggleExtension(id, enabled) {
  await api(`extensions/${encodeURIComponent(id)}/update`, { enabled });
  await loadExtensions(id);
}
async function trustExtensions(trusted) {
  await api('extensions/trust', { trusted });
  await loadExtensions(state.extensionId);
}
function extensionFormValue() {
  const root = $('#capabilities-view');
  const parseObject = (selector, label) => {
    const raw = root.querySelector(selector)?.value.trim() || '{}';
    let value;
    try { value = JSON.parse(raw); } catch { throw new Error(`${label}必须是 JSON 对象。`); }
    if (!value || Array.isArray(value) || typeof value !== 'object') throw new Error(`${label}必须是 JSON 对象。`);
    return value;
  };
  return {
    required: root.querySelector('#extension-required')?.checked ?? false,
    settings: parseObject('#extension-settings', '设置'),
    env_from: parseObject('#extension-env-from', '环境变量映射'),
  };
}
async function saveExtension() {
  if (!state.extensionId) return;
  await api(`extensions/${encodeURIComponent(state.extensionId)}/update`, extensionFormValue());
  await loadExtensions(state.extensionId);
}
async function restartExtension() {
  if (!state.extensionId) return;
  await api(`extensions/${encodeURIComponent(state.extensionId)}/restart`, {});
  await loadExtensions(state.extensionId);
  error('扩展已重启。');
}
async function showExtensionLogs() {
  if (!state.extensionId) return;
  const response = await fetch(`/api/extensions/${encodeURIComponent(state.extensionId)}/logs`, {
    headers: { 'X-FreeAgent-Key': key }, cache: 'no-store',
  });
  if (!response.ok) throw new Error('无法读取扩展日志。');
  const root = $('#extension-log');
  if (root) { root.hidden = false; root.textContent = await response.text(); }
}
async function clearExtensionData() {
  if (!state.extensionId || !window.confirm(`清空扩展“${state.extensionId}”的数据？`)) return;
  await api(`extensions/${encodeURIComponent(state.extensionId)}/clear-data`, {});
  error('扩展数据已清空。');
}
async function deleteSkill(name) {
  if (!window.confirm(`删除 Skill“${name}”？`)) return;
  await api(`skills/${encodeURIComponent(name)}/delete`, {});
  await loadSkills();
  error(`Skill“${name}”已删除。`);
}
async function saveMCP() {
  const form = $('#mcp-form');
  if (!form?.reportValidity()) return;
  const value = mcpFormValue();
  const id = state.mcpServer || $('#mcp-id')?.value.trim() || '';
  if (!id) throw new Error('缺少 MCP 服务器名称。');
  if (state.mcpServer) await api(`mcp/${encodeURIComponent(id)}/update`, value);
  else await api('mcp', { id, config: value });
  await loadMCP(id);
}
async function testMCP() {
  const value = mcpFormValue();
  const id = state.mcpServer || $('#mcp-id')?.value.trim() || '';
  if (!id) throw new Error('请先填写 MCP 服务器名称。');
  const result = await api(`mcp/${encodeURIComponent(id)}/test`, value);
  error(result.connected ? `MCP 连接成功，发现 ${result.tool_count || 0} 个工具。` : 'MCP 连接未建立。');
}
async function deleteMCP() {
  if (!state.mcpServer || !window.confirm(`删除 MCP“${state.mcpServer}”？`)) return;
  await api(`mcp/${encodeURIComponent(state.mcpServer)}/delete`, {});
  state.mcpServer = '';
  await loadMCP();
}
async function restartMCP() {
  await api('mcp/restart', {});
  await loadMCP(state.mcpServer);
  error('MCP 已重启。');
}
async function showMCPLogs() {
  if (!state.mcpServer) return;
  const response = await fetch(`/api/mcp/${encodeURIComponent(state.mcpServer)}/logs`, {
    headers: { 'X-FreeAgent-Key': key }, cache: 'no-store',
  });
  if (!response.ok) throw new Error('无法读取 MCP 日志。');
  const root = $('#mcp-log');
  if (root) { root.hidden = false; root.textContent = await response.text(); }
}
function openChatPage() {
  state.page = 'chat';
  syncPage();
}
function draft() {
  if (hasView()) safeStorage(storage, draftKey(), $('#prompt').value);
  if (state.preview) {
    $('#draft-preview').innerHTML = renderMarkdown($('#prompt').value);
    decorate($('#draft-preview'));
  }
  controls();
}
function renderRunBar() {
  const turn = latest();
  const bar = $('#run-bar');
  if (!turn) {
    bar.hidden = true;
    $('#run-status').replaceChildren();
    $('#run-details').replaceChildren();
    return;
  }
  const meta = stateMeta(turn.state);
  let elapsed = turn.elapsed_ms || 0;
  if (state.busy && state.runAnchor?.key === turn.key) {
    elapsed = state.runAnchor.base + Math.max(0, Date.now() - state.runAnchor.at);
  }
  bar.hidden = false;
  bar.dataset.state = turn.state;
  $('#run-status').innerHTML = `${icon(meta.icon)}<strong>${esc(meta.label)}</strong>`;
  $('#run-status').dataset.state = turn.state;
  $('#run-details').innerHTML = `<span>${turn.model_id ? esc(turn.model_id) : '本地模型'}</span><span>${turn.steps} 步</span><span>用时 ${elapsedText(elapsed)}</span>${turn.runtime_revision ? `<span>配置 revision ${esc(turn.runtime_revision)}</span>` : ''}${turn.enabled_tools?.length ? `<span>工具：${esc(turn.enabled_tools.join('、'))}</span>` : ''}${turn.reason ? `<span class="run-reason" title="${esc(turn.reason)}">${esc(reasonText(turn.reason))}</span>` : ''}${state.runNotice ? `<span class="run-notice">${esc(state.runNotice)}</span>` : ''}`;
  decorate(bar);
}
function controls() {
  setDesktopBusy(desktopBusy);
  const turn = latest(), pending = state.pending;
  const noModels = !hasModels(state.env);
  const mode = submitMode(turn, state.busy || state.elsewhere);
  $('#send').disabled = !state.online || noModels || !hasView() || state.mutation || state.creating || !!pending ||
    mode === 'blocked' || !$('#prompt').value.trim();
  $('#send').classList.toggle('end-send', mode === 'end_and_send');
  $('#send').title = mode === 'end_and_send' ? '结束并发送' : '发送';
  $('#send').setAttribute('aria-label', mode === 'end_and_send' ? '结束并发送' : '发送');
  $('#send-label').textContent = mode === 'end_and_send' ? '结束并发送' : '发送';
  $('#model-trigger').disabled = !state.env || noModels || state.creating;
  $('#permission-trigger').disabled = !state.env || state.creating;
  $('#workspace-change').disabled = state.mutation || desktopBusy;
  $('#prompt').disabled = state.workspaceChanging;
  $('#input-state').innerHTML = noModels ? '请先添加 Key 和模型' :
    state.elsewhere && state.activeChat && state.activeChat !== state.chat
      ? `<button class="text-action active-chat-link" data-chat="${esc(state.activeChat)}">另一会话正在运行</button>`
      : state.elsewhere ? '另一会话正在运行' : pending ? '正在核对提交' : state.creating ? '正在创建对话' : '';
  const actions = [];
  if (state.busy) actions.push(button('square', state.stopping ? '正在停止' : '停止任务', 'stop',
    state.stopping || state.mutation || !state.online ? 'disabled' : ''));
  if (!state.busy && !state.elsewhere && turn && !turn.ended) {
    if (turn.state === 'awaiting_approval') actions.push(`<button class="text-action" data-action="approval" ${state.online ? '' : 'disabled'}>查看待确认变更</button>`);
    else if (turn.can_continue) actions.push(button('play', '继续任务', 'continue', state.online ? '' : 'disabled'));
    if (turn.state === 'needs_attention') actions.push(button('settings2', '调整限额', 'limits', state.online ? '' : 'disabled'));
    if (turn.can_end) actions.push(button('x', '结束任务', 'ask-end', state.online ? '' : 'disabled'));
  }
  if (pending) {
    actions.push('<button class="text-action" data-action="check-submit">核对提交</button>');
    if (!state.busy && state.online) actions.push('<button class="text-action" data-action="keep-draft">保留草稿</button>');
  }
  const html = actions.join('');
  if ($('#run-controls').controlSignature !== html || $('#run-controls').mutation !== state.mutation) {
    $('#run-controls').innerHTML = html;
    $('#run-controls').controlSignature = html;
    $('#run-controls').mutation = state.mutation;
    $('#run-controls').querySelectorAll('button').forEach((item) => { if (state.mutation) item.disabled = true; });
    decorate($('#run-controls'));
  }
  renderRunBar();
  renderMemoryPanel();
  if (state.busy && state.runAnchor) {
    clearTimeout(runTimer);
    runTimer = setTimeout(() => {
      runTimer = 0;
      renderRunBar();
      controls();
    }, 1000);
  }
}
async function listChats() {
  state.chats = await api('chats');
  renderChatList();
}
async function togglePin(chatID, pinned) {
  if (!chatID || state.mutation) return;
  await api(`chats/${encodeURIComponent(chatID)}/pin`, { pinned: Boolean(pinned) });
  await listChats();
}
async function newChat() {
  if (state.mutation || state.creating) return;
  openChatPage();
  if (hasView()) draft();
  resetView();
  state.chat = null;
  state.drafting = true;
  state.pending = null;
  state.pendingModel = '';
  state.pendingPermission = '';
  state.permissionConfirmed = false;
  $('#prompt').value = safeStorage(storage, draftKey());
  safeStorage(storage, selectedKey(), '');
  $('#title').textContent = '新对话';
  setTitleAttention(false);
  renderModelMenu();
  renderTurns();
  renderStream();
  controls();
  await listChats();
  if (state.preview) draft();
  $('#prompt').focus();
}
async function selectChat(id) {
  if (state.mutation || state.creating) return;
  await openChat(id);
}
function resetView() {
  state.epoch++;
  state.turns = [];
  state.view = null;
  state.streams.clear();
  state.raw.clear();
  state.approval = null;
  state.approvalControl = null;
  state.approvalTab = 'items';
  state.approvalNotice = '';
  state.approvalLoading = false;
  state.expanded.clear();
  state.following = true;
  state.busy = false;
  state.stopping = false;
  state.elsewhere = false;
  state.activeChat = '';
  state.runAnchor = null;
  state.runNotice = '';
  state.endIntent = 'end';
  state.endPrompt = '';
  state.earlierLoading = false;
  state.modelMenuOpen = false;
  state.modelMenuIndex = -1;
  state.permissionMenuOpen = false;
  state.permissionDialogMode = '';
  state.pendingCapabilities = null;
  state.capabilityMenuOpen = false;
  state.commandMenuOpen = false;
  state.commandIndex = -1;
  state.pendingCommand = null;
  state.memory = null;
  state.memoryOpen = false;
  earlierVisible = false;
  earlierObserver?.disconnect();
  earlierObserver = null;
  clearTimeout(runTimer);
  runTimer = 0;
  $('#limits').close();
  $('#ending').close();
  $('#permission-dialog').close();
  $('#workflow-input-dialog').close();
  $('.workspace').classList.remove('drawer-open');
  renderModelMenu();
  renderCapabilityMenu();
  renderMemoryPanel();
  error('');
}
async function openChat(id) {
  openChatPage();
  if (hasView()) draft();
  resetView();
  state.chat = id;
  state.drafting = false;
  try { state.pending = JSON.parse(safeStorage(storage, pendingKey()) || 'null'); } catch { state.pending = null; }
  $('#prompt').value = safeStorage(storage, draftKey());
  safeStorage(storage, selectedKey(), id);
  await snapshot();
  await listChats();
  if (state.preview) draft();
}
// Creates the chat for the first send. The input moves to the new chat's
// draft only after creation succeeds; any failure keeps it where it was.
async function createChat(prompt) {
  const requestedModel = state.pendingModel;
  const requestedPermission = state.pendingPermission;
  const requestedPermissionConfirmed = state.permissionConfirmed;
  const requestedCapabilities = state.pendingCapabilities;
  state.creating = true;
  error('');
  controls();
  try {
    const chat = await api('chats', { title: chatTitle(prompt) });
    safeStorage(storage, chatDraftKey(chat.id), $('#prompt').value);
    safeStorage(storage, draftKey(), '');
    state.drafting = false;
    await openChat(chat.id);
    state.pendingModel = requestedModel;
    state.pendingPermission = requestedPermission;
    state.permissionConfirmed = requestedPermissionConfirmed;
    state.pendingCapabilities = requestedCapabilities;
    renderModelMenu();
    renderPermissionMenu();
    renderCapabilityMenu();
    if (requestedModel) {
      try {
        await chatAPI('model', { model: requestedModel });
        state.pendingModel = '';
        await snapshot();
      } catch (failure) {
        error(failure.message || '模型选择未保存，已保留当前选择。');
        return false;
      }
    }
    if (requestedPermission) {
      try {
        await chatAPI('permission', {
          mode: requestedPermission,
          confirmed: requestedPermissionConfirmed,
        });
        state.pendingPermission = '';
        state.permissionConfirmed = false;
        await snapshot();
      } catch (failure) {
        error(failure.message || '权限选择未保存，已保留当前选择。');
        return false;
      }
    }
    if (requestedCapabilities) {
      try {
        await chatAPI('capabilities', requestedCapabilities);
        state.pendingCapabilities = null;
        await snapshot();
      } catch (failure) {
        error(failure.message || '能力选择未保存，已保留当前选择。');
        return false;
      }
    }
    return true;
  } catch (failure) {
    if (state.chat) {
      error(failure.definitive ? failure.message : '连接中断，请刷新记录。草稿已保留。');
      queueSnapshot();
    } else error(failure.definitive ? failure.message : '连接中断，对话可能未创建。草稿已保留。');
    return false;
  } finally {
    state.creating = false;
    controls();
  }
}
function savePending(value) {
  state.pending = value;
  safeStorage(storage, pendingKey(), value ? JSON.stringify(value) : '');
  controls();
}
function readerAnchor() {
  const reader = $('#reader'), top = reader.getBoundingClientRect().top;
  const child = [...$('#turns').children].find((node) => node.getBoundingClientRect().bottom >= top);
  return { node: child?.dataset.turn, offset: child ? child.getBoundingClientRect().top - top : 0,
    scroll: reader.scrollTop, height: reader.scrollHeight };
}
function restoreAnchor(anchor, earlier = false) {
  const reader = $('#reader');
  scrollProgram = true;
  if (state.following && !earlier) reader.scrollTop = reader.scrollHeight;
  else {
    const node = [...$('#turns').children].find((child) => child.dataset.turn === anchor.node);
    reader.scrollTop = node ? reader.scrollTop + node.getBoundingClientRect().top - reader.getBoundingClientRect().top - anchor.offset
      : earlier ? anchor.scroll + reader.scrollHeight - anchor.height : anchor.scroll;
  }
  requestAnimationFrame(() => { scrollProgram = false; });
  $('#latest').hidden = state.following;
}
function observeEarlier() {
  const button = $('#earlier');
  if (!button || button.hidden || !state.view?.has_earlier || !state.view.before || !state.chat) {
    earlierObserver?.disconnect();
    earlierObserver = null;
    earlierVisible = false;
    return;
  }
  if (earlierObserver) return;
  earlierObserver = new IntersectionObserver((entries) => {
    const entry = entries[0];
    if (!entry) return;
    if (!entry.isIntersecting) {
      earlierVisible = false;
      return;
    }
    if (earlierVisible || state.earlierLoading) return;
    earlierVisible = true;
    loadEarlier().catch((failure) => error(failure.message || '无法加载更早的消息。'));
  }, { root: $('#reader'), rootMargin: '160px 0px', threshold: 0 });
  earlierObserver.observe(button);
}
async function loadEarlier() {
  if (state.earlierLoading || !state.view?.has_earlier || !state.view.before) return;
  const epoch = state.epoch;
  state.earlierLoading = true;
  const button = $('#earlier');
  if (button) {
    button.disabled = true;
    button.textContent = '正在加载更早的消息...';
  }
  try {
    state.following = false;
    await snapshot(state.view.before);
  } finally {
    if (epoch === state.epoch) {
      state.earlierLoading = false;
      if (button) {
        button.disabled = false;
        button.textContent = '加载更早的消息';
      }
    }
  }
}
async function snapshot(before = '') {
  const chat = state.chat, lane = before ? 'earlier' : 'current';
  const ticket = { chat, epoch: state.epoch, lane, before,
    revision: ++state.refresh[lane], generation: ++state.refresh.generation };
  const streamRevision = state.streamRevision;
  if (!chat) return;
  const result = await api(`chats/${encodeURIComponent(chat)}${before ? `?before=${encodeURIComponent(before)}` : ''}`);
  if (!isCurrentSnapshot(ticket, state)) return;
  const anchor = readerAnchor();
  const view = result.view;
  view.turns = restoreRawBodies(view.turns, state.raw);
  if (before) {
    const seen = new Set(state.turns.map((turn) => turn.key));
    state.turns = [...view.turns.filter((turn) => !seen.has(turn.key)), ...state.turns];
    state.view.has_earlier = view.has_earlier;
    state.view.before = view.before;
  } else {
    const currentKeys = new Set(view.turns.map((turn) => turn.key));
    state.turns = [...state.turns.filter((turn) => !currentKeys.has(turn.key)), ...view.turns];
    const prior = state.view;
    state.view = view;
    if (prior && state.turns.length > view.turns.length) {
      state.view.has_earlier = prior.has_earlier;
      state.view.before = prior.before;
    }
    const previousActiveChat = state.activeChat;
    state.busy = result.busy;
    state.stopping = result.stopping;
    state.elsewhere = result.running_elsewhere;
    state.activeChat = result.active_chat || '';
    if (state.busy && view.turns.at(-1)?.state === 'generating') {
      const current = view.turns.at(-1);
      state.runAnchor = { key: current.key, base: current.elapsed_ms || 0, at: Date.now() };
    } else {
      state.runAnchor = null;
      clearTimeout(runTimer);
      runTimer = 0;
    }
    if (previousActiveChat !== state.activeChat) listChats().catch(() => {});
    reportDesktopBusy(state.busy || state.elsewhere, true);
    if (streamRevision === state.streamRevision) {
      state.streams = new Map(result.streams.map((stream) => [stream.attempt, stream]));
    }
    if (state.pending && state.turns.some((turn) => turn.request_key === state.pending.requestKey)) {
      if ($('#prompt').value === state.pending.prompt) {
        $('#prompt').value = '';
        safeStorage(storage, draftKey(), '');
      }
      savePending(null);
    }
    $('#title').textContent = view.conversation.title;
    setTitleAttention(titleAttention);
    renderModelMenu();
    renderPermissionMenu();
    if (state.approval && (latest()?.key !== state.approval.run_id || latest()?.state !== 'awaiting_approval')) {
      state.approval = null;
      state.approvalControl = null;
      state.approvalNotice = '';
    }
  }
  renderTurns();
  renderStream();
  if (!before) loadMemory().catch((failure) => error(failure.message));
  controls();
  restoreAnchor(anchor, !!before);
  observeEarlier();
  ensureApprovalPreview();
}
function receiptHTML(receipt) {
  const label = receipt.path || receipt.command || receipt.url || '运行记录';
  const kind = receipt.kind === 'command' ? 'terminal' : receipt.kind === 'http' ? 'link2' : 'file-check2';
  const hashHTML = (label, value) => value
    ? `<span class="hash" title="${esc(value)}">${esc(label)} ${esc(shortHash(value))}<button class="icon hash-copy" data-copy="${encodeURIComponent(value)}" title="复制完整哈希" aria-label="复制完整哈希">${icon('copy')}</button></span>`
    : '';
  return `<li class="receipt">${icon(kind)}<div><strong>${esc(label)}</strong><span class="receipt-state">${esc(statusText(receipt.status))}${receipt.exit_code === undefined ? '' : ` · 退出码 ${receipt.exit_code}`}</span>${hashHTML('修改前', receipt.before_hash)}${hashHTML('修改后', receipt.after_hash)}${receipt.working_dir ? `<span class="receipt-state">工作目录：${esc(receipt.working_dir)}</span>` : ''}${receipt.http_status ? `<span class="receipt-state">HTTP ${receipt.http_status}</span>` : ''}${receipt.detail ? `<p>${esc(receipt.detail)}</p>` : ''}${receipt.truncated ? '<p class="warning">输出已截断</p>' : ''}</div></li>`;
}
function activityHTML(activity, turnKey) {
  const meta = activityLabel(activity.kind === 'model' ? 'model' : activity.name || activity.kind);
  const failed = activity.is_error || ['failed', 'unknown'].includes(activity.status);
  const bodyKey = `${turnKey}:body:${activity.key}`;
  return `<li><div class="activity-line"><span class="activity-name">${icon(meta.icon)}<strong>${esc(meta.label)}</strong>${activity.target ? `<span class="activity-target" title="${esc(activity.target)}">${esc(activity.target)}</span>` : ''}</span><span class="${failed ? 'warning' : ''}">${esc(statusText(failed && activity.status === 'finished' ? 'failed' : activity.status))}</span></div>${activity.failure ? `<p class="warning">${esc(providerFailureText(activity.failure))}</p>` : ''}${activity.text ? `<details data-section="${esc(bodyKey)}" ${state.expanded.has(bodyKey) ? 'open' : ''}><summary>${activity.kind === 'tool' ? '工具返回' : '未完成响应'}</summary><pre class="tool-output">${esc(activity.text)}</pre>${activity.truncated ? `<button class="text-action" data-full-activity="${esc(activity.key)}">加载完整返回</button>` : ''}</details>` : ''}${activity.receipts?.length ? `<ul class="receipts">${activity.receipts.map(receiptHTML).join('')}</ul>` : ''}</li>`;
}
function activitySummary(turn) {
  const recent = turn.activity.at(-1);
  if (!recent) return '';
  const meta = activityLabel(recent.kind === 'model' ? 'model' : recent.name || recent.kind);
  const failures = turn.activity.filter((activity) => activity.is_error || ['failed', 'unknown'].includes(activity.status)).length;
  return `${turn.activity.length} 项 · ${meta.label}${recent.target ? ` · ${recent.target}` : ''}${failures ? ` · ${failures} 项失败` : ''}`;
}
function section(turn, name, title, content) {
  const id = `${turn.key}:${name}`;
  return `<details data-section="${esc(id)}" ${state.expanded.has(id) ? 'open' : ''}><summary>${icon('chevron-right')}${title}</summary><div class="section-body">${content}</div></details>`;
}
function turnHTML(turn) {
  let html = turn.messages.map((message) => `<article class="message ${esc(message.role)}" data-message="${esc(message.key)}"><div class="message-heading"><span>${message.role === 'user' ? '你' : 'FreeAgent'}</span><button type="button" class="icon" data-copy-message="${esc(message.key)}" title="复制原始 Markdown" aria-label="复制原始 Markdown">${icon('copy')}</button></div><div class="markdown">${message.role === 'user' ? `<p class="user-text">${esc(message.raw)}</p>` : renderMarkdown(message.raw)}</div>${message.truncated ? `<button class="text-action" data-full-message="${esc(message.key)}">加载完整原文</button>` : ''}</article>`).join('');
  html += `<div class="turn-state" data-state="${esc(turn.state)}">${icon(stateMeta(turn.state).icon)}${esc(stateMeta(turn.state).label)}${turn.ended ? '<span>任务已结束</span>' : ''}${turn.model_id ? `<span>${esc(turn.model_id)}</span>` : ''}</div>`;
  if (turn.state === 'unknown') html += '<div class="warning-strip">结果未知，可能已有作用。请核对执行记录；未自动重试。</div>';
  else if (turn.state === 'stopped') html += '<div class="subtle-note">任务已停止，已有操作会保留。</div>';
  if (turn.activity.length) {
    html += section(turn, 'activity', `运行活动 <span class="count">${esc(activitySummary(turn))}</span>`, `<ol class="activities">${turn.activity.map((activity) => activityHTML(activity, turn.key)).join('')}</ol>`);
  }
  if (turn.sources.length) {
    html += section(turn, 'sources', `来源与上下文 <span class="count">${turn.sources.length}</span>`, `<ul class="sources">${turn.sources.map((source) => `<li><strong>${esc(source.title || source.scope?.owner || '显式上下文')}</strong><span>${esc(source.scope?.kind || '')}${source.revision ? ` · 版本 ${source.revision}` : ''}${source.state ? ` · ${esc(source.state)}` : ''}</span>${source.excerpt ? `<span>片段 ${source.excerpt.start}–${source.excerpt.end}</span>` : ''}</li>`).join('')}</ul>`);
  }
  const evidence = turn.evidence || {}, groups = evidenceGroups(evidence);
  if (groups.length) {
    html += section(turn, 'evidence', '执行证据', groups.map((group) => {
      if (group.key === 'checks') {
        return `<div class="evidence-group"><h4>${esc(group.label)}</h4>${group.items.map((item) => item.status === 'passed'
          ? `<p class="check">${icon('check')}${esc(item.value)} · 运行通过</p>`
          : `<p class="warning">${esc(item.value)} · 未运行</p>`).join('')}</div>`;
      }
      if (group.key === 'uncertainty') {
        return `<div class="evidence-group uncertainty-group"><h4>${esc(group.label)}</h4>${group.items.map((item) => `<p class="warning">${esc(item)}</p>`).join('')}</div>`;
      }
      return `<div class="evidence-group"><h4>${esc(group.label)}</h4><ul class="receipts">${group.items.map(receiptHTML).join('')}</ul></div>`;
    }).join(''));
  }
  if (turn.state === 'awaiting_approval' && turn.key === latest()?.key) {
    html += '<div id="approval-slot"></div>';
  }
  return html;
}
function renderApproval() {
  const slot = $('#approval-slot');
  if (!slot) return;
  const signature = JSON.stringify([state.approvalNotice, state.approvalTab, state.approval?.digest,
    state.approvalControl?.code, latest()?.key]);
  if (slot.signature === signature) return;
  slot.signature = signature;
  if (state.approvalNotice) {
    slot.innerHTML = `<section class="approval-card"><header tabindex="-1"><div><h3>需要重新预览</h3><span>待确认操作已发生变化</span></div></header><div class="approval-body"><p class="warning">${esc(state.approvalNotice)}</p></div><footer><button class="primary" data-action="approval">${icon('refresh-cw')}重新预览</button></footer></section>`;
  } else if (!state.approval || state.approval.run_id !== latest()?.key) {
    slot.innerHTML = '<section class="approval-card approval-loading"><p>正在加载待确认变更...</p></section>';
  } else {
    slot.innerHTML = state.approvalTab === 'raw' ? approvalRawHTML(state.approval)
      : approvalHTML(state.approval, { control: state.approvalControl });
  }
  decorate(slot);
}
function renderTurns() {
  const started = performance.now();
  const container = $('#turns');
  if (!state.turns.length) container.replaceChildren();
  const keys = new Set(state.turns.map((turn) => turn.key));
  for (const child of [...container.children]) if (!keys.has(child.dataset.turn)) child.remove();
  const nodes = new Map([...container.children].map((child) => [child.dataset.turn, child]));
  let cursor = container.firstElementChild;
  for (const turn of state.turns) {
    let node = nodes.get(turn.key);
    const signature = turnSignature(turn);
    if (!node) {
      node = document.createElement('section');
      node.className = 'turn';
      node.dataset.turn = turn.key;
    }
    if (node.signature !== signature) {
      node.innerHTML = turnHTML(turn);
      node.signature = signature;
      decorate(node);
    }
    if (node !== cursor) container.insertBefore(node, cursor);
    cursor = node.nextElementSibling;
  }
  $('#empty').hidden = !!state.turns.length;
  if (!state.turns.length) {
    const empty = hasModels(state.env)
      ? `<span class="empty-mark" aria-hidden="true">${icon('message-square')}</span><h2>新对话</h2>`
      : `<span class="empty-mark" aria-hidden="true">${icon('key-round')}</span><h2>先添加一个 Key 和模型</h2><button class="primary" data-action="keys">${icon('key-round')}添加 Key</button>`;
    $('#empty').innerHTML = empty;
    decorate($('#empty'));
  }
  $('#earlier').hidden = !state.view?.has_earlier;
  $('#earlier').disabled = state.earlierLoading;
  $('#earlier').textContent = state.earlierLoading ? '正在加载更早的消息...' : '加载更早的消息';
  renderApproval();
  measured('fa:turns', started);
}
function streamPart(raw, tail = false) {
  return tail && new TextEncoder().encode(raw).length > (64 << 10)
    ? `<pre class="stream-raw">${esc(raw)}</pre>`
    : renderMarkdown(raw, { highlight: !tail });
}
function renderStream() {
  const started = performance.now();
  const holder = $('#stream'), stream = [...state.streams.values()].at(-1);
  if (!stream) {
    holder.replaceChildren();
    delete holder.streamAttempt;
    delete holder.streamStable;
    measured('fa:stream', started);
    return;
  }
  const anchor = readerAnchor();
  const parts = splitStable(stream.raw);
  if (holder.streamAttempt !== stream.attempt) {
    holder.innerHTML = `<article class="message assistant streaming"><div class="message-heading"><span>FreeAgent</span><span class="stream-label"></span></div><div class="markdown stream-stable"></div><div class="markdown stream-tail"></div><div class="stream-warning"></div></article>`;
    holder.streamAttempt = stream.attempt;
    holder.streamStable = '';
    decorate(holder);
  }
  const stable = holder.querySelector('.stream-stable');
  const tail = holder.querySelector('.stream-tail');
  if (!parts.stable.startsWith(holder.streamStable)) {
    stable.innerHTML = streamPart(parts.stable);
    holder.streamStable = parts.stable;
  } else if (parts.stable.length > holder.streamStable.length) {
    stable.insertAdjacentHTML('beforeend', streamPart(parts.stable.slice(holder.streamStable.length)));
    holder.streamStable = parts.stable;
  }
  tail.innerHTML = streamPart(parts.tail, true);
  holder.querySelector('.stream-label').textContent = stream.interrupted ? '未完成片段' : '生成中';
  holder.querySelector('.stream-warning').innerHTML =
    `${stream.interrupted ? '<p class="warning">此片段不是最终回答，权威结果以运行记录为准。</p>' : ''}${stream.truncated ? '<p class="warning">临时显示已达到限额，保存后的原文以记录为准。</p>' : ''}`;
  decorate(holder);
  restoreAnchor(anchor);
  measured('fa:stream', started);
}
function queueSnapshot() {
  if (snapshotTimer) return;
  snapshotTimer = setTimeout(() => {
    snapshotTimer = 0;
    snapshot().catch((failure) => error(failure.message));
  }, 180);
}
function connectEvents() {
  sse?.close();
  sse = new EventSource('/api/events');
  sse.onopen = () => { connection(true); queueSnapshot(); };
  sse.onerror = () => connection(false);
  sse.onmessage = (message) => {
    let event;
    try { event = JSON.parse(message.data); } catch { error('事件格式无效，请刷新记录。'); return; }
    if (event.kind === 'heartbeat') return;
    if (event.kind === 'reconnect') { queueSnapshot(); return; }
    if (event.kind === 'confirmation' || event.kind === 'control_confirmation') {
      if (!document.hasFocus()) setTitleAttention(true);
      notifyDesktopAttention(event.run_id);
    }
    if (event.conversation_id && event.conversation_id !== state.chat) {
      clearTimeout(refreshListTimer);
      refreshListTimer = setTimeout(() => listChats().catch(() => {}), 300);
      if (event.kind !== 'model_text') queueSnapshot();
      return;
    }
    if (event.kind === 'request_error') error(event.message.slice(event.message.indexOf(': ') + 2));
    if (event.kind.startsWith('model_') && event.attempt_id) {
      state.streamRevision++;
      const reduced = reduceStream(state.streams.get(event.attempt_id), event);
      if (event.kind === 'model_started' && !event.purpose) state.streams.clear();
      if (reduced) state.streams.set(event.attempt_id, reduced);
      else state.streams.delete(event.attempt_id);
      if (!streamTimer) streamTimer = setTimeout(() => { streamTimer = 0; renderStream(); }, 80);
      if (event.kind === 'model_text') return;
    }
    queueSnapshot();
  };
}
async function mutate(action, body = {}) {
  if (state.mutation) return;
  state.mutation = true;
  if (['submit', 'continue', 'confirm', 'workflow'].includes(action)) reportDesktopBusy(true);
  error('');
  controls();
  let accepted = false;
  try {
    const result = await chatAPI(action, body);
    accepted = true;
    await snapshot();
    return result;
  } catch (failure) {
    // A successful POST followed by a failed refresh leaves the outcome
    // uncertain; callers must retain their deduplication record.
    if (accepted) failure.definitive = false;
    throw failure;
  } finally { state.mutation = false; controls(); }
}
async function submitPrompt(prompt, retainDraftOnFailure = false) {
  const permission = state.pendingPermission;
  const confirmFullAccess = state.permissionConfirmed;
  if (!state.chat) {
    if (!await createChat(prompt)) return false;
    if (!canSubmit(latest(), state.busy || state.elsewhere)) return false;
  }
  savePending({ requestKey: crypto.randomUUID(), prompt });
  draft();
  try {
    await mutate('submit', { prompt, request_key: state.pending.requestKey,
      ...(permission ? { permission, confirm_full_access: confirmFullAccess } : {}),
      ...(state.pendingCapabilities ? { capabilities: state.pendingCapabilities } : {}) });
    state.runNotice = '';
    state.pendingPermission = '';
    state.permissionConfirmed = false;
    state.pendingCapabilities = null;
    renderPermissionMenu();
    renderCapabilityMenu();
    return true;
  } catch (failure) {
    if (failure.definitive) savePending(null);
    $('#prompt').value = prompt;
    draft();
    error(retainDraftOnFailure
      ? (failure.definitive ? '上一任务已结束，消息未发送，可以直接重新发送。' : '上一任务已结束，提交结果尚未确定。草稿已保留，请先核对记录。')
      : (failure.definitive ? failure.message : '连接中断，提交结果尚未确定。草稿已保留，请先核对记录。'));
    queueSnapshot();
    return false;
  }
}
async function runWorkflow(entry, input) {
  if (!entry || entry.kind !== 'workflow' || !state.online || state.mutation || state.creating ||
    state.busy || state.elsewhere || state.pending) return false;
  const prompt = `/${entry.name}`;
  if (!state.chat) {
    if (!await createChat(prompt)) return false;
    if (!canSubmit(latest(), state.busy || state.elsewhere)) return false;
  }
  const requestKey = crypto.randomUUID();
  savePending({ requestKey, prompt, command: `${entry.extension}/${entry.name}` });
  closeCommandMenu();
  draft();
  try {
    await mutate('workflow', {
      extension: entry.extension,
      workflow: entry.name,
      input,
      request_key: requestKey,
    });
    state.runNotice = '';
    return true;
  } catch (failure) {
    if (failure.definitive) savePending(null);
    error(failure.definitive
      ? failure.message
      : '连接中断，Workflow 提交结果尚未确定。请先核对记录，不要直接重试。');
    queueSnapshot();
    return false;
  }
}
function endEvidenceSummary(turn) {
  const groups = evidenceGroups(turn?.evidence || {});
  if (!groups.length) return '<p>没有发现工具证据。结束不会撤销已发生的作用。</p>';
  return `<p>已有操作和未知结果会保留。结束不会撤销已发生的作用。</p><ul class="end-evidence">${groups.map((group) =>
    `<li><strong>${esc(group.label)}</strong><span>${group.items.length} 项</span></li>`).join('')}</ul>`;
}
function openEndDialog(submit = false) {
  const turn = latest();
  state.endIntent = submit ? 'end_and_send' : 'end';
  state.endPrompt = submit ? $('#prompt').value : '';
  const risk = endRisk(turn);
  $('#ending-content').innerHTML = risk === 'high'
    ? '<p class="warning">上一任务可能已经产生作用但结果未知，结束不会撤销任何作用。</p>'
    : endEvidenceSummary(turn);
  const ackLine = $('#end-ack-line');
  const ack = $('#end-ack');
  ack.checked = false;
  ackLine.hidden = risk !== 'high';
  ack.disabled = risk !== 'high';
  const confirmButton = $('#ending [data-action="end"]');
  confirmButton.disabled = risk === 'high';
  confirmButton.dataset.risk = risk;
  $('#ending').showModal();
}
async function finishEnd() {
  const turn = latest();
  const submit = state.endIntent === 'end_and_send';
  const prompt = state.endPrompt;
  if (endRisk(turn) === 'high' && !$('#end-ack').checked) return;
  $('#ending').close();
  try {
    await mutate('end', { acknowledge: true });
    state.runNotice = '已结束上一任务';
    if (submit) await submitPrompt(prompt, true);
  } catch (failure) {
    if (submit) {
      $('#prompt').value = prompt;
      draft();
    }
    error(failure.message || '结束任务未完成。');
  } finally {
    state.endIntent = 'end';
    state.endPrompt = '';
  }
}
async function send() {
  if ($('#send').disabled) return;
  const prompt = $('#prompt').value;
  if (new TextEncoder().encode(prompt).length > state.env.max_prompt_bytes) {
    error('任务超过当前输入限额，草稿已保留。');
    return;
  }
  const mode = submitMode(latest(), state.busy || state.elsewhere);
  if (mode === 'end_and_send') {
    if (endRisk(latest()) === 'low') {
      state.endIntent = 'end_and_send';
      state.endPrompt = prompt;
      await finishEnd();
    } else openEndDialog(true);
    return;
  }
  await submitPrompt(prompt);
}
async function previewApproval() {
  if (state.approvalLoading) return;
  const chat = state.chat;
  state.approvalLoading = true;
  try {
    const value = await chatAPI('preview', {});
    if (chat !== state.chat || latest()?.key !== value.run_id) return;
    // A model control proposal is confirmed with the owner-rendered code.
    // Rendering again in the same owner generation returns the same code.
    let control = null;
    if (value.control_operation_id) {
      control = await api(`control/confirmation?operation_id=${encodeURIComponent(value.control_operation_id)}&conversation_id=${encodeURIComponent(chat)}`);
      if (chat !== state.chat || latest()?.key !== value.run_id) return;
    }
    state.approval = value;
    state.approvalControl = control;
    state.approvalTab = 'items';
    state.approvalNotice = '';
    renderTurns();
    requestAnimationFrame(() => $('#approval-slot header')?.focus());
  } finally {
    state.approvalLoading = false;
  }
}
function ensureApprovalPreview() {
  const turn = latest();
  if (!state.online || state.approvalLoading || state.approvalNotice ||
    turn?.state !== 'awaiting_approval') {
    if (turn?.state !== 'awaiting_approval') state.approvalLoading = false;
    return;
  }
  if (state.approval?.run_id === turn.key) return;
  previewApproval().catch((failure) => error(failure.message || '无法读取待确认变更。'));
}
async function confirm(approved) {
  if (!state.approval || state.mutation) return;
  const confirmation = state.approval;
  const control = state.approvalControl;
  state.approval = null;
  state.approvalControl = null;
  renderApproval();
  const body = { confirmation, approved };
  if (confirmation.control_operation_id && control?.operation_id === confirmation.control_operation_id) body.code = control.code;
  await mutate('confirm', body);
}
async function loadRaw(kind, id) {
  return (await chatAPI(`raw?kind=${kind}&key=${encodeURIComponent(id)}`)).raw;
}
async function copy(raw, source) {
  try { await navigator.clipboard.writeText(raw); }
  catch { error('无法访问剪贴板，原文未改动。'); return; }
  source.title = '已复制';
  source.setAttribute('aria-label', '已复制');
}
async function saveKey() {
  const form = $('#key-form');
  if (!form.reportValidity()) return;
  rememberKeyDraft();
  const value = keyForm();
  const path = value.id ? `keys/${encodeURIComponent(value.id)}/update` : 'keys';
  const saved = await api(path, value);
  state.keyDrafts.delete(value.id);
  if (!value.id && state.keyProbe) state.keyModels.set(saved.id, state.keyProbe);
  state.keyProbe = null;
  state.keyProfile = saved.id;
  await loadKeys(saved.id);
  await refreshEnvironment();
  renderKeysView();
}
async function testKey() {
  rememberKeyDraft();
  const value = keyForm();
  if (value.source === '') {
    error('环境变量来源由 CLI 管理，界面不能直接测试。');
    return;
  }
  const profile = selectedKeyProfile();
  const unchangedSavedProfile = profile && value.id && !value.key &&
    value.name === profile.name && value.base_url === profile.base_url &&
    value.note === profile.note && value.protocol === profile.protocol && value.source === profile.source;
  if (!unchangedSavedProfile && !value.base_url.trim()) {
    error('请先填写服务地址。');
    return;
  }
  const result = await api('keys/test', unchangedSavedProfile ? { id: value.id } : value);
  if (value.id) state.keyModels.set(value.id, result);
  else state.keyProbe = result;
  const selected = value.id || state.keyProfile;
  if (value.id) {
    await loadKeys(selected);
  } else {
    renderKeysView();
  }
  state.keyNotice = result.category === 'ok' ? `连接成功，共 ${result.models?.length || 0} 个模型。` : `连接测试：${result.category}${result.detail ? `，${result.detail}` : ''}`;
  renderKeysView();
}
async function fetchKeyModels() {
  const profile = selectedKeyProfile();
  if (!profile) return;
  const result = await api(`keys/${encodeURIComponent(profile.id)}/models`, {});
  state.keyModels.set(profile.id, result);
  await loadKeys(profile.id);
  state.keyNotice = result.category === 'ok' ? `已获取 ${result.models?.length || 0} 个模型。` : `模型列表：${result.category}`;
  renderKeysView();
}
async function enableKeyModels(models) {
  const profile = selectedKeyProfile();
  const names = [...new Set((models || []).map((model) => String(model).trim()).filter(Boolean))];
  if (!profile || !names.length) return;
  await api('models/enable-batch', { key: profile.id, models: names });
  await refreshEnvironment();
  renderKeysView();
}
async function enableKeyModel(model, id = '', makeDefault = false) {
  const profile = selectedKeyProfile();
  if (!profile || !model.trim()) return;
  const existing = modelsForKey(state.env, profile.id).find((item) => item.model === model);
  await api('models/enable', {
    key: profile.id, model: model.trim(), id: id || existing?.id || '', default: makeDefault || !hasModels(state.env),
  });
  await refreshEnvironment();
  renderKeysView();
}
async function setDefaultModel(id) {
  if (!id || !state.env.models[id]) return;
  await api('models/default', { id });
  await refreshEnvironment();
  renderKeysView();
}
async function disableKeyModel(id) {
  const model = state.env.models[id];
  let newDefault = '';
  if (state.env.default_model === id && Object.keys(state.env.models).length > 1) {
    const options = Object.keys(state.env.models).filter((item) => item !== id).join(', ');
    newDefault = window.prompt(`停用默认模型。请选择新的默认模型 ID：${options}`, options.split(', ')[0]) || '';
    if (!newDefault) return;
  }
  await api(`models/${encodeURIComponent(id)}/disable`, { new_default: newDefault });
  await refreshEnvironment();
  renderKeysView();
}
async function deleteKey() {
  const profile = selectedKeyProfile();
  if (!profile) return;
  const affected = modelsForKey(state.env, profile.id);
  let disableModels = false, newDefault = '';
  if (affected.length) {
    const names = affected.map((item) => item.id).join(', ');
    if (!window.confirm(`此 Key 正被模型使用：${names}\n同时停用这些模型并删除 Key？`)) return;
    disableModels = true;
    const remaining = Object.keys(state.env.models).filter((id) => !affected.some((item) => item.id === id));
    if (affected.some((item) => item.id === state.env.default_model) && remaining.length) {
      newDefault = window.prompt(`删除默认模型。请选择新的默认模型 ID：${remaining.join(', ')}`, remaining[0]) || '';
      if (!newDefault) return;
    }
  } else if (!window.confirm(`删除 Key“${profile.name}”？`)) return;
  await api(`keys/${encodeURIComponent(profile.id)}/delete`, { disable_models: disableModels, new_default: newDefault });
  state.keyModels.delete(profile.id);
  state.keyDrafts.delete(profile.id);
  state.keyProfile = '';
  await loadKeys();
  await refreshEnvironment();
  renderKeysView();
}
async function excludeMemory(encoded, knowledge = false) {
  const value = JSON.parse(decodeURIComponent(encoded));
  if (!window.confirm('从本会话后续任务中移除此项？已完成任务的记录不会改变。')) return;
  await chatAPI('exclude-memory', knowledge ? { knowledge: value } : { entry: value });
  await loadMemory();
}
function applyPreset(id) {
  const preset = (state.keys?.presets || []).find((item) => item.id === id);
  if (!preset) return;
  $('#key-name').value = preset.name;
  $('#key-base-url').value = preset.base_url;
  $('#key-none').checked = false;
  if ($('#key-protocol')) $('#key-protocol').value = preset.protocol || 'openai-compatible';
  $('#key-secret').focus();
}
async function act(action, node = null) {
  switch (action) {
    case 'sidebar':
      if (matchMedia(DRAWER_QUERY).matches) {
        const open = $('.workspace').classList.toggle('drawer-open');
        if (open) requestAnimationFrame(() => $('#chat-search')?.focus());
      } else $('.workspace').classList.toggle('sidebar-collapsed');
      break;
    case 'new': await newChat(); break;
    case 'keys': await openKeys(); break;
    case 'capabilities': await openCapabilities(); break;
    case 'settings': await openSettings(); break;
    case 'permissions': await openPermissions(); break;
    case 'close-permissions': closePermissions(); break;
    case 'save-command-policy': await saveCommandPolicy(); break;
    case 'save-computer-files': await saveComputerFiles(); break;
    case 'save-settings-permission': await saveSettingsPermission(); break;
    case 'save-settings-tools': await saveSettingsTools(); break;
    case 'add-permission-root': await addPermissionRoot(); break;
    case 'add-permission-folder': await addPermissionRoot('folder'); break;
    case 'add-permission-file': await addPermissionRoot('file'); break;
    case 'remove-permission-root': removePermissionRoot(node?.dataset.root || ''); break;
    case 'add-blocked-folder': await addBlockedPath('folder'); break;
    case 'add-blocked-file': await addBlockedPath('file'); break;
    case 'remove-blocked-path': removeBlockedPath(node?.dataset.path || ''); break;
    case 'save-permission-page': await savePermissionPage(); break;
    case 'rename': openRenameDialog(); break;
    case 'close-rename': closeRenameDialog(); break;
    case 'save-rename': await saveRename(); break;
    case 'skill-import': await importSkill(); break;
    case 'skill-toggle': await toggleSkill(node?.dataset.skillId || '', node?.dataset.skillEnabled === 'true'); break;
    case 'skill-trust': await trustSkills(node?.dataset.skillTrusted === 'true'); break;
    case 'skill-delete': await deleteSkill(node?.dataset.skillId || ''); break;
    case 'extension-import': await importExtension(); break;
    case 'extension-toggle': await toggleExtension(node?.dataset.extensionId || '', node?.dataset.extensionEnabled === 'true'); break;
    case 'extension-trust': await trustExtensions(node?.dataset.extensionTrusted === 'true'); break;
    case 'extension-save': await saveExtension(); break;
    case 'extension-restart': await restartExtension(); break;
    case 'extension-logs': await showExtensionLogs(); break;
    case 'extension-clear-data': await clearExtensionData(); break;
    case 'memory-toggle':
      state.memoryOpen = !state.memoryOpen;
      renderMemoryPanel();
      break;
    case 'key-new':
      state.keyProfile = '';
      state.keyProbe = null;
      state.keyNotice = '';
      state.keyModelQuery = '';
      renderKeysView();
      $('#key-name')?.focus();
      break;
    case 'key-save': await saveKey(); break;
    case 'key-test': await testKey(); break;
    case 'key-models': await fetchKeyModels(); break;
    case 'key-delete': await deleteKey(); break;
    case 'key-enable': await enableKeyModel(node?.dataset.model || ''); break;
    case 'key-enable-manual':
      state.keyManualModel = $('#manual-model')?.value || '';
      await enableKeyModel(state.keyManualModel);
      state.keyManualModel = '';
      renderKeysView();
      break;
    case 'key-enable-all': await enableKeyModels((state.keyModels.get(selectedKeyProfile()?.id)?.models || state.keyProbe?.models || []).filter((model) => !modelsForKey(state.env, selectedKeyProfile()?.id).some((item) => item.model === model))); break;
    case 'key-default': await setDefaultModel(node?.dataset.modelId || ''); break;
    case 'key-disable': await disableKeyModel(node?.dataset.modelId || ''); break;
    case 'mcp-new':
      state.mcpServer = '';
      renderCapabilitiesView();
      $('#mcp-id')?.focus();
      break;
    case 'mcp-save': await saveMCP(); break;
    case 'mcp-test': await testMCP(); break;
    case 'mcp-delete': await deleteMCP(); break;
    case 'mcp-restart': await restartMCP(); break;
    case 'mcp-logs': await showMCPLogs(); break;
    case 'refresh': await snapshot(); await listChats(); break;
    case 'dismiss': error(''); break;
    case 'latest': state.following = true; restoreAnchor(readerAnchor()); break;
    case 'send': await send(); break;
    case 'close-workflow-input': closeWorkflowInput(); break;
    case 'run-workflow-input': await runWorkflowInput(); break;
    case 'stop': await mutate('stop'); break;
    case 'continue': await mutate('continue'); break;
    case 'approval': await previewApproval(); break;
    case 'approval-raw': state.approvalTab = 'raw'; renderApproval(); break;
    case 'approval-items': state.approvalTab = 'items'; renderApproval(); break;
    case 'approve': await confirm(true); break;
    case 'reject': await confirm(false); break;
    case 'ask-end': openEndDialog(false); break;
    case 'close-end': $('#ending').close(); break;
    case 'close-permission-dialog': closePermissionDialog(); break;
    case 'confirm-permission-dialog': confirmPermissionDialog(); break;
    case 'end': await finishEnd(); break;
    case 'check-submit': await snapshot(); break;
    case 'keep-draft':
      await snapshot();
      if (state.pending && !state.busy) {
        savePending(null);
        error('当前记录未发现此提交。草稿已保留，没有自动重新发送。');
      }
      break;
    case 'edit': case 'preview':
      state.preview = action === 'preview';
      $('#prompt').hidden = state.preview;
      $('#draft-preview').hidden = !state.preview;
      $('[data-action="edit"]').setAttribute('aria-pressed', String(!state.preview));
      $('[data-action="preview"]').setAttribute('aria-pressed', String(state.preview));
      draft();
      if (!state.preview) $('#prompt').focus();
      break;
    case 'limits': {
      const limits = await chatAPI('limits');
      $('#limit-steps').value = limits.max_steps;
      $('#limit-seconds').value = limits.max_run_seconds;
      $('#limit-steps').min = limits.max_steps;
      $('#limit-seconds').min = limits.max_run_seconds;
      $('#limits').showModal();
      break;
    }
    case 'close-limits': $('#limits').close(); break;
    case 'save-limits':
      if (!$('#limit-steps').reportValidity() || !$('#limit-seconds').reportValidity()) return;
      await mutate('limits', { steps: Number($('#limit-steps').value), seconds: Number($('#limit-seconds').value) });
      $('#limits').close();
      break;
  }
}
document.addEventListener('click', async (event) => {
  const node = event.target.closest('button, [data-action]');
  if (!node || node.disabled) return;
  try {
    if (node.dataset.action === 'chat-pin') {
      await togglePin(node.dataset.chat, node.dataset.pinned !== 'true');
    } else if (node.dataset.chat) {
      await selectChat(node.dataset.chat);
      if (matchMedia(DRAWER_QUERY).matches) {
        $('.workspace').classList.remove('drawer-open');
        $('#model-trigger')?.focus();
      }
    }
    else if (node.dataset.mcpId) {
      state.mcpServer = node.dataset.mcpId;
      renderCapabilitiesView();
    }
    else if (node.dataset.skillId && !node.dataset.action) {
      await loadSkillDetail(node.dataset.skillId);
    }
    else if (node.dataset.extensionId && !node.dataset.action) {
      state.extensionId = node.dataset.extensionId;
      renderCapabilitiesView();
    }
    else if (node.dataset.capabilityTab) {
      state.capabilityTab = node.dataset.capabilityTab;
      if (state.capabilityTab === 'skills') await loadSkills();
      else if (state.capabilityTab === 'mcp') await loadMCP();
      else if (state.capabilityTab === 'extensions') await loadExtensions();
      else renderCapabilitiesView();
    }
    else if (node.dataset.capabilityId) {
      await toggleCapability(node.dataset.capabilityGroup, node.dataset.capabilityId);
    }
    else if (node.dataset.commandIndex !== undefined) {
      const entries = visibleCommands();
      await chooseCommand(entries[Number(node.dataset.commandIndex)]);
    }
    else if (node.dataset.modelId && !node.dataset.action) await chooseModel(node.dataset.modelId);
    else if (node.dataset.modelMenuAction) await chooseModel(node.dataset.modelMenuAction);
    else if (node.dataset.memoryEntry) await excludeMemory(node.dataset.memoryEntry);
    else if (node.dataset.memoryKnowledge) await excludeMemory(node.dataset.memoryKnowledge, true);
    else if (node.dataset.copy) await copy(decodeURIComponent(node.dataset.copy), node);
    else if (node.dataset.copyMessage) {
      const message = state.turns.flatMap((turn) => turn.messages).find((item) => item.key === node.dataset.copyMessage);
      await copy(message.truncated ? await loadRaw('message', message.key) : message.raw, node);
    } else if (node.dataset.fullMessage || node.dataset.fullActivity) {
      const isMessage = !!node.dataset.fullMessage, id = node.dataset.fullMessage || node.dataset.fullActivity;
      const chat = state.chat, epoch = state.epoch;
      const raw = await loadRaw(isMessage ? 'message' : 'activity', id);
      if (chat !== state.chat || epoch !== state.epoch) return;
      const anchor = readerAnchor();
      if (isMessage) {
        const message = state.turns.flatMap((turn) => turn.messages).find((item) => item.key === id);
        if (!message) return;
        state.raw.set(`message:${id}`, raw);
        message.raw = raw;
        message.truncated = false;
        renderTurns();
      } else {
        const activity = state.turns.flatMap((turn) => turn.activity).find((item) => item.key === id);
        if (!activity) return;
        if (activity.status === 'finished') state.raw.set(`activity:${id}`, raw);
        activity.text = raw;
        activity.truncated = false;
        renderTurns();
      }
      restoreAnchor(anchor);
    } else if (node.dataset.keyId) {
      state.keyProfile = node.dataset.keyId;
      state.keyNotice = '';
      state.keyModelQuery = '';
      state.keyManualModel = '';
      renderKeysView();
    } else if (node.dataset.action === 'earlier') {
      await loadEarlier();
    } else await act(node.dataset.action, node);
  } catch (failure) {
    if (failure.code === 'reconfirm') {
      state.approval = null;
      state.approvalControl = null;
      state.approvalNotice = '变更已变化。请重新预览并确认。';
      renderTurns();
    }
    error(failure.message || '连接中断，请核对已有记录。');
    queueSnapshot();
  }
});
document.addEventListener('toggle', (event) => {
  if (!event.target.dataset.section) return;
  const sectionKey = event.target.dataset.section;
  if (event.target.open) {
    state.expanded.add(sectionKey);
    state.following = false;
    $('#latest').hidden = false;
  } else state.expanded.delete(sectionKey);
}, true);
$('#prompt').addEventListener('input', () => {
  draft();
  updateCommandMenu();
});
$('#prompt').addEventListener('compositionstart', () => { state.composing = true; });
$('#prompt').addEventListener('compositionend', () => {
  state.composing = false;
  draft();
  updateCommandMenu();
});
$('#prompt').addEventListener('keydown', (event) => {
  if (state.commandMenuOpen && ['ArrowDown', 'ArrowUp'].includes(event.key)) {
    event.preventDefault();
    const entries = visibleCommands();
    if (entries.length) {
      state.commandIndex = (state.commandIndex + (event.key === 'ArrowDown' ? 1 : -1) + entries.length) % entries.length;
      renderCommandMenu();
    }
    return;
  }
  if (state.commandMenuOpen && event.key === 'Enter' && !event.shiftKey && !state.composing) {
    event.preventDefault();
    chooseCommand();
    return;
  }
  if (event.key === 'Escape' && state.commandMenuOpen) {
    event.preventDefault();
    closeCommandMenu();
    return;
  }
  if (shouldSend(event, state.composing, $('#send').disabled, !!$('#prompt').value.trim())) {
    event.preventDefault();
    send();
  }
});
document.addEventListener('keydown', (event) => {
  if (event.key !== 'Escape') return;
  if (state.commandMenuOpen) {
    event.preventDefault();
    closeCommandMenu();
    return;
  }
  if (state.pendingCommand) {
    event.preventDefault();
    closeWorkflowInput();
    return;
  }
  if (state.modelMenuOpen) {
    event.preventDefault();
    closeModelMenu();
    return;
  }
  if (state.permissionMenuOpen) {
    event.preventDefault();
    closePermissionMenu();
    return;
  }
  if (state.permissionDialogMode) {
    event.preventDefault();
    closePermissionDialog();
    return;
  }
  if (state.page === 'permissions' || state.page === 'settings') {
    event.preventDefault();
    closePermissions();
    return;
  }
  if (matchMedia(DRAWER_QUERY).matches && $('.workspace').classList.contains('drawer-open')) {
    event.preventDefault();
    $('.workspace').classList.remove('drawer-open');
    $('[data-action="sidebar"]')?.focus();
  }
  if (state.chatQuery) {
    state.chatQuery = '';
    $('#chat-search').value = '';
    renderChatList();
  }
});
$('#reader').addEventListener('scroll', () => {
  if (scrollProgram) return;
  state.following = followsLatest($('#reader').scrollTop, $('#reader').scrollHeight, $('#reader').clientHeight);
  $('#latest').hidden = state.following;
});
$('#model-trigger').addEventListener('click', () => {
  if (state.modelMenuOpen) closeModelMenu();
  else openModelMenu();
});
$('#permission-trigger').addEventListener('click', () => {
  if (state.permissionMenuOpen) closePermissionMenu();
  else {
    state.permissionMenuOpen = true;
    renderPermissionMenu();
  }
});
$('#permission-menu').addEventListener('click', (event) => {
  const option = event.target.closest('[data-permission-id]');
  if (option) choosePermission(option.dataset.permissionId).catch((failure) => error(failure.message || '权限模式未保存。'));
});
$('#capability-trigger').addEventListener('click', () => {
  state.capabilityMenuOpen = !state.capabilityMenuOpen;
  renderCapabilityMenu();
});
$('#capability-menu').addEventListener('keydown', (event) => {
  if (event.key === 'Escape') {
    event.preventDefault();
    closeCapabilityMenu();
  }
});
$('#permission-trigger').addEventListener('keydown', (event) => {
  if (!['Enter', ' ', 'ArrowDown', 'ArrowUp'].includes(event.key)) return;
  event.preventDefault();
  state.permissionMenuOpen = true;
  renderPermissionMenu();
  const options = [...$('#permission-menu').querySelectorAll('[data-permission-id]')];
  const selected = options.findIndex((option) => option.dataset.permissionId === selectedPermissionMode());
  const delta = event.key === 'ArrowUp' ? -1 : event.key === 'ArrowDown' ? 1 : 0;
  (options[(selected + delta + options.length) % options.length] || options[0])?.focus();
});
$('#permission-menu').addEventListener('keydown', (event) => {
  const options = [...$('#permission-menu').querySelectorAll('[data-permission-id]')];
  if (event.key === 'Escape') {
    event.preventDefault();
    closePermissionMenu();
    return;
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    const current = options.indexOf(document.activeElement);
    options[(current + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length]?.focus();
    return;
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    const option = document.activeElement?.closest('[data-permission-id]');
    if (option) choosePermission(option.dataset.permissionId);
  }
});
$('#model-trigger').addEventListener('keydown', (event) => {
  if (!['Enter', ' ', 'ArrowDown', 'ArrowUp'].includes(event.key)) return;
  event.preventDefault();
  if (!state.modelMenuOpen) {
    openModelMenu().then(() => {
      if (event.key === 'ArrowUp') {
        const items = modelMenuItems(state.env, state.keys?.profiles || []);
        state.modelMenuIndex = nextIndex(items, state.modelMenuIndex, -1);
        renderModelMenu();
      }
    });
    return;
  }
  const items = modelMenuItems(state.env, state.keys?.profiles || []);
  if (event.key === 'Enter' || event.key === ' ') chooseModel(items[state.modelMenuIndex]?.id);
  else {
    state.modelMenuIndex = nextIndex(items, state.modelMenuIndex, event.key === 'ArrowDown' ? 1 : -1);
    renderModelMenu();
  }
});
$('#model-menu').addEventListener('keydown', (event) => {
  const items = modelMenuItems(state.env, state.keys?.profiles || []);
  if (event.key === 'Escape') { event.preventDefault(); closeModelMenu(); return; }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    state.modelMenuIndex = nextIndex(items, state.modelMenuIndex, event.key === 'ArrowDown' ? 1 : -1);
    renderModelMenu();
  } else if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    chooseModel(items[state.modelMenuIndex]?.id);
  }
});
$('#chat-search').addEventListener('input', (event) => {
  state.chatQuery = event.target.value;
  renderChatList();
});
$('#chat-search').addEventListener('keydown', (event) => {
  if (event.key === 'Escape') {
    event.preventDefault();
    state.chatQuery = '';
    event.target.value = '';
    renderChatList();
  }
});
$('#keys-view').addEventListener('change', (event) => {
  if (event.target.id === 'key-preset') applyPreset(event.target.value);
  if (event.target.id === 'key-none' && event.target.checked) {
    const secret = $('#key-secret');
    if (secret) secret.value = '';
  }
  if (event.target.id === 'key-preset' || event.target.closest('#key-form')) rememberKeyDraft();
});
$('#keys-view').addEventListener('input', (event) => {
  if (event.target.closest('#key-form')) {
    rememberKeyDraft();
    return;
  }
  if (event.target.id === 'manual-model') {
    state.keyManualModel = event.target.value;
    return;
  }
  if (event.target.id !== 'key-model-search') return;
  state.keyModelQuery = event.target.value;
  renderKeysView();
  $('#key-model-search')?.focus();
});
$('#end-ack').addEventListener('change', () => {
  const button = $('#ending [data-action="end"]');
  if (button.dataset.risk === 'high') button.disabled = !$('#end-ack').checked;
});
window.addEventListener('online', () => { connectEvents(); queueSnapshot(); });
window.addEventListener('beforeunload', () => { draft(); sse?.close(); });
window.addEventListener('focus', () => {
  setTitleAttention(false);
  refreshEnvironment().catch(() => {});
  loadKeys().catch(() => {});
  loadCommandCatalog().catch(() => {});
});
const drawerMedia = matchMedia(DRAWER_QUERY);
drawerMedia.addEventListener?.('change', () => {
  $('.workspace').classList.remove('drawer-open');
});
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) {
    queueSnapshot();
    refreshEnvironment().catch(() => {});
    loadKeys().catch(() => {});
    loadCommandCatalog().catch(() => {});
  }
});

async function boot() {
  decorate();
  if (!key) { error('请使用本次启动输出的本地 Chat 地址。'); connection(false); return; }
  await api('connect', {});
  await refreshEnvironment();
  await loadKeys();
  await loadCommandCatalog();
  await listChats();
  // Reopen the last chat; otherwise return to an unsent new-chat draft, then
  // to the latest chat. Nothing is created until the first send.
  const selected = safeStorage(storage, selectedKey());
  if (state.chats.some((chat) => chat.id === selected)) await selectChat(selected);
  else if (state.chats.length && !safeStorage(storage, `freeagent:${state.env.environment}:draft:new`)) await selectChat(state.chats[0].id);
  else await newChat();
  connectEvents();
  syncPage();
}
boot().catch((failure) => { connection(false); error(failure.message || '连接未完成。'); });
