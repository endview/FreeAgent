export const STATES = {
  generating: '生成中', awaiting_approval: '等待确认', stopped: '已停止',
  needs_attention: '需要关注', completed: '已完成', failed: '失败', unknown: '结果未知',
};
export const DRAWER_QUERY = '(max-width: 959px)';

export function matchChat(chat, query) {
  const needle = String(query ?? '').normalize('NFKC').toLocaleLowerCase();
  if (!needle) return true;
  return String(chat?.title ?? '').normalize('NFKC').toLocaleLowerCase().includes(needle);
}

export function groupChats(chats = [], now = new Date()) {
  const current = new Date(now);
  current.setHours(0, 0, 0, 0);
  const yesterday = current.getTime() - 24 * 60 * 60 * 1000;
  const recent = current.getTime() - 6 * 24 * 60 * 60 * 1000;
  const groups = [
    ['今天', []], ['昨天', []], ['近 7 天', []], ['更早', []],
  ];
  const pinned = chats.filter((chat) => chat?.pinned);
  const normal = chats.filter((chat) => !chat?.pinned);
  for (const chat of normal) {
    const timestamp = Date.parse(chat?.updated_at || '');
    const time = Number.isFinite(timestamp) ? timestamp : 0;
    if (time >= current.getTime()) groups[0][1].push(chat);
    else if (time >= yesterday) groups[1][1].push(chat);
    else if (time >= recent) groups[2][1].push(chat);
    else groups[3][1].push(chat);
  }
  const result = groups.filter(([, items]) => items.length).map(([label, items]) => ({ label, items }));
  return pinned.length ? [{ label: '置顶', items: pinned }, ...result] : result;
}

export function prioritizePinned(chats = []) {
  return [...chats.filter((chat) => chat?.pinned), ...chats.filter((chat) => !chat?.pinned)];
}

export function attentionTitle(base, waiting) {
  const title = String(base || 'FreeAgent Chat');
  return waiting ? `（等待确认）${title}` : title;
}

export function permissionMode(view, pending = '') {
  return pending || view?.permission?.mode || 'ask';
}

const STATE_META = {
  generating: { label: STATES.generating, icon: 'loader-circle', token: 'run' },
  awaiting_approval: { label: STATES.awaiting_approval, icon: 'hand', token: 'wait' },
  stopped: { label: STATES.stopped, icon: 'square', token: 'stop' },
  needs_attention: { label: STATES.needs_attention, icon: 'settings-2', token: 'warn' },
  completed: { label: STATES.completed, icon: 'circle-check', token: 'done' },
  failed: { label: STATES.failed, icon: 'circle-x', token: 'fail' },
  unknown: { label: STATES.unknown, icon: 'circle-question-mark', token: 'unknown' },
};

export function stateMeta(state) {
  return STATE_META[state] || { ...STATE_META.needs_attention };
}

const REASONS = {
  'user confirmation required': '等待用户确认',
  'process interrupted; continue explicitly': '进程中断，需要明确继续',
  'unresolved call after restart': '重启后仍有未确定调用',
  'could not check unresolved operations': '无法核对未确定操作',
  'saved synthesis failed; child outcomes retained': '汇总失败，子任务结果已保留',
  'expert suggestion requires user review': '专家建议需要用户审核',
  'authorized expert plan saved; explicit foreground continuation': '已保存授权方案，需要前台明确继续',
  'replanning failed; saved child results retained': '重新规划失败，已保存子任务结果',
  'conflicting runtime evidence; inspect and resolve exact digest': '运行证据冲突，需要核对具体摘要',
};

export function reasonText(reason) {
  const value = String(reason ?? '');
  return REASONS[value] || value;
}

const ACTIVITY_META = {
  command_run: ['运行命令', 'terminal'],
  http_get: ['请求网址', 'globe'],
  file_read: ['读取文件', 'file-text'],
  file_list: ['列出目录', 'folder-tree'],
  file_search: ['搜索文件', 'file-search'],
  file_edit: ['修改文件', 'file-pen'],
  file_create: ['创建文件', 'file-plus'],
  file_delete: ['删除文件', 'trash-2'],
  file_batch_edit: ['批量修改文件', 'files'],
  apply_patch: ['应用补丁', 'files'],
  memory_list: ['列出记忆', 'brain'],
  memory_read: ['读取记忆', 'book-open'],
  memory_write: ['写入记忆', 'brain'],
  knowledge_search: ['检索知识', 'search'],
  model: ['模型生成', 'bot'],
  assessment: ['模型生成', 'bot'],
  planning: ['模型生成', 'bot'],
  compaction: ['压缩上下文', 'shrink'],
  context_manage: ['管理上下文', 'shrink'],
};

export function activityLabel(name) {
  const key = String(name ?? '');
  if (ACTIVITY_META[key]) {
    const [label, icon] = ACTIVITY_META[key];
    return { label, icon };
  }
  if (key.startsWith('mcp_')) return { label: '调用工具', icon: 'plug-zap' };
  return { label: key || '运行活动', icon: 'plug-zap' };
}

export function submitMode(turn, busy) {
  if (busy) return 'blocked';
  if (!turn || turn.ended || ['completed', 'failed'].includes(turn.state)) return 'send';
  return turn.can_end ? 'end_and_send' : 'blocked';
}

const READ_ONLY_TOOLS = new Set([
  'file_read', 'file_list', 'file_search', 'memory_list', 'memory_read', 'knowledge_search',
]);

export function endRisk(turn) {
  if (!turn) return 'low';
  if (turn.state === 'unknown' ||
    (turn.activity || []).some((activity) => ['unknown', 'dispatched'].includes(activity.status))) {
    return 'high';
  }
  const evidence = turn.evidence || {};
  if (turn.state === 'awaiting_approval' ||
    (evidence.files || []).length || (evidence.commands_run || []).length ||
    (evidence.http_requests || []).length ||
    (turn.activity || []).some((activity) => activity.kind === 'tool' && !READ_ONLY_TOOLS.has(activity.name))) {
    return 'medium';
  }
  return 'low';
}

export function evidenceGroups(evidence = {}) {
  const groups = [];
  const add = (key, label, items) => {
    if (items?.length) groups.push({ key, label, items });
  };
  add('files', '文件', evidence.files);
  add('commands', '命令', evidence.commands_run);
  add('http', '网络', evidence.http_requests);
  if ((evidence.checks_passed || []).length || (evidence.checks_not_run || []).length) {
    groups.push({
      key: 'checks',
      label: '检查',
      items: [...(evidence.checks_passed || []).map((value) => ({ value, status: 'passed' })),
        ...(evidence.checks_not_run || []).map((value) => ({ value, status: 'not_run' }))],
    });
  }
  add('uncertainty', '仍不确定', evidence.remaining_uncertainty);
  return groups;
}

export function shortHash(hash) {
  const value = String(hash ?? '');
  return value.length > 8 ? value.slice(0, 8) : value;
}

// Rendering only depends on fields that change the visible turn structure.
// Runtime clocks stay outside this signature; explicit raw-body loads do not.
export function turnSignature(turn = {}) {
  const evidence = turn.evidence || {};
  const evidenceCounts = [
    ['files', evidence.files],
    ['commands_run', evidence.commands_run],
    ['http_requests', evidence.http_requests],
    ['checks_passed', evidence.checks_passed],
    ['checks_not_run', evidence.checks_not_run],
    ['remaining_uncertainty', evidence.remaining_uncertainty],
  ].map(([key, items]) => [key, Array.isArray(items) ? items.length : 0]);
  return JSON.stringify({
    key: turn.key || '',
    state: turn.state || '',
    reason: turn.reason || '',
    can_continue: Boolean(turn.can_continue),
    can_end: Boolean(turn.can_end),
    ended: Boolean(turn.ended),
    model: turn.model_id || '',
    confirmation: turn.confirmation?.call_id || '',
    messages: (turn.messages || []).map((message) => [
      message.key || '', String(message.raw ?? '').length, Boolean(message.truncated),
    ]),
    activity: (turn.activity || []).map((activity) => [
      activity.key || '', activity.status || '', String(activity.text ?? '').length,
      Boolean(activity.truncated), Boolean(activity.is_error),
    ]),
    evidence: evidenceCounts,
    sources: Array.isArray(turn.sources) ? turn.sources.length : 0,
  });
}

export function providerFailureText(failure) {
  if (!failure) return '';
  if (!failure.Certain) return 'Provider 结果未知，可能已处理请求；请核对记录，不要直接重试。';
  return ({
    authentication: 'Provider 拒绝了这个 Key，请在 Key 管理中检查或重新输入。',
    permission: 'Provider 拒绝访问，请检查模型与凭据权限。',
    quota: 'Provider 额度不足，请处理额度后再发起新任务。',
    rate_limit: 'Provider 限流。稍后可显式发起新任务；未自动重试。',
    context_limit: 'Provider 上下文限额不足，请缩小输入或选择合适模型。',
    capability: 'Provider 未满足声明的模型能力，未派发返回的工具。',
    usage: 'Provider 未提供声明要求的用量信息，未派发返回的工具。',
    output_limit: 'Provider 输出达到限额，片段不是最终回答，未执行其中的工具。',
    refusal: 'Provider 拒绝了请求，未执行其中的工具。',
    content_filter: 'Provider 过滤了请求，未执行其中的工具。',
    request: 'Provider 拒绝请求，请检查模型配置与协议。',
    redirect: 'Provider 要求重定向，未转发凭据或自动跟随。',
    unavailable: 'Provider 暂时不可用。可显式发起新任务；未自动重试。',
    timeout: 'Provider 返回超时。可显式发起新任务；未自动重试。',
    cancelled: '模型派发前已停止，实际操作记录仍会保留。',
  })[failure.category] || 'Provider 返回确定性失败，请核对运行记录。';
}

export function shouldSend(event, composing, busy, hasText) {
  return event.key === 'Enter' && !event.shiftKey && !event.isComposing &&
    event.keyCode !== 229 && !composing && !busy && hasText;
}

export function followsLatest(scrollTop, height, clientHeight) {
  return height - scrollTop - clientHeight < 80;
}

export function isCurrentSnapshot(ticket, state) {
  return ticket.chat === state.chat && ticket.epoch === state.epoch &&
    ticket.generation === state.refresh.generation &&
    ticket.revision === state.refresh[ticket.lane] &&
    (ticket.lane !== 'earlier' || ticket.before === state.view?.before);
}

// Explicitly loaded immutable bodies survive bounded presentation snapshots.
export function mergeCurrentTurns(previous, current) {
  const keys = new Set(current.map((turn) => turn.key));
  return [...previous.filter((turn) => !turn.maintenance && !keys.has(turn.key)), ...current];
}

export function restoreRawBodies(turns, raw) {
  return turns.map((turn) => ({
    ...turn,
    messages: turn.messages.map((message) => raw.has(`message:${message.key}`)
      ? { ...message, raw: raw.get(`message:${message.key}`), truncated: false } : message),
    activity: turn.activity.map((activity) => activity.status === 'finished' && raw.has(`activity:${activity.key}`)
      ? { ...activity, text: raw.get(`activity:${activity.key}`), truncated: false } : activity),
  }));
}

export function canSubmit(turn, busy) {
  return !busy && (!turn || turn.ended || ['completed', 'failed'].includes(turn.state));
}

// Title for a chat created by its first send: first non-blank line, trimmed,
// at most 80 code points (the server counts runes the same way).
export function chatTitle(text) {
  const line = String(text ?? '').split(/\r\n|\r|\n/).map((item) => item.trim()).find(Boolean) || '';
  return Array.from(line).slice(0, 80).join('');
}

// Stream display is temporary and never upgrades a runtime outcome.
export function reduceStream(current, event) {
  if (event.purpose) return current;
  if (event.kind === 'model_started') return { attempt: event.attempt_id, run: event.run_id, raw: '', interrupted: false };
  if (event.attempt_id !== current?.attempt) return current;
  if (event.kind === 'model_text') {
    if (current.raw.length + event.message.length > 1 << 20) return { ...current, truncated: true };
    return { ...current, raw: current.raw + event.message };
  }
  if (event.kind === 'model_finished') return null;
  if (event.kind === 'model_interrupted') return { ...current, interrupted: true };
  return current;
}

export function safeStorage(storage, key, value) {
  try {
    if (value === undefined) return storage.getItem(key) || '';
    if (value === '') storage.removeItem(key);
    else storage.setItem(key, value);
    return value;
  } catch { return value === undefined ? '' : value; }
}
