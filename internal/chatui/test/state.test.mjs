import test from 'node:test';
import assert from 'node:assert/strict';
import { shouldSend, followsLatest, canSubmit, reduceStream, safeStorage, isCurrentSnapshot, restoreRawBodies, providerFailureText, chatTitle,
  stateMeta, activityLabel, submitMode, endRisk, evidenceGroups, shortHash, turnSignature, groupChats, matchChat, attentionTitle,
  permissionMode, prioritizePinned } from '../src/state.mjs';

test('Enter, Shift and Chinese IME', () => {
  assert.equal(shouldSend({ key: 'Enter' }, false, false, true), true);
  for (const event of [{ key: 'Enter', shiftKey: true }, { key: 'Enter', isComposing: true }, { key: 'Enter', keyCode: 229 }, { key: 'a' }]) assert.equal(shouldSend(event, false, false, true), false);
  assert.equal(shouldSend({ key: 'Enter' }, true, false, true), false);
  assert.equal(shouldSend({ key: 'Enter' }, false, true, true), false);
});
test('scroll follow suspends and can resume', () => {
  assert.equal(followsLatest(0, 1200, 600), false);
  assert.equal(followsLatest(590, 1200, 600), true);
});
test('snapshot lanes reject old revisions and mismatched history cursors', () => {
  const state = { chat: 'chat', epoch: 4, refresh: { current: 8, earlier: 2, generation: 9 }, view: { before: 'run' } };
  const ticket = { chat: 'chat', epoch: 4, lane: 'current', revision: 8, generation: 9 };
  assert.equal(isCurrentSnapshot(ticket, state), true);
  assert.equal(isCurrentSnapshot({ ...ticket, revision: 7 }, state), false);
  assert.equal(isCurrentSnapshot({ ...ticket, chat: 'other' }, state), false);
  assert.equal(isCurrentSnapshot({ ...ticket, epoch: 3 }, state), false);
  const earlier = { ...ticket, lane: 'earlier', revision: 2, before: 'run', generation: 9 };
  assert.equal(isCurrentSnapshot(earlier, state), true);
  assert.equal(isCurrentSnapshot({ ...earlier, revision: 1 }, state), false);
  assert.equal(isCurrentSnapshot({ ...earlier, before: 'old-run' }, state), false);
  assert.equal(isCurrentSnapshot({ ...earlier, generation: 8 }, state), false);
});
test('complete raw bodies survive refresh without mutating input or overwriting live progress', () => {
  const turns = [{ key: 'r', messages: [{ key: 'm', raw: 'short', truncated: true }],
    activity: [{ key: 'a', status: 'finished', text: 'short', truncated: true },
      { key: 'b', status: 'dispatched', text: 'new progress', truncated: true }] }];
  const raw = new Map([['message:m', 'exact markdown'], ['activity:a', 'exact tool return'], ['activity:b', 'old progress']]);
  const restored = restoreRawBodies(turns, raw);
  assert.equal(restored[0].messages[0].raw, 'exact markdown');
  assert.equal(restored[0].messages[0].truncated, false);
  assert.equal(restored[0].activity[0].text, 'exact tool return');
  assert.equal(restored[0].activity[1].text, 'new progress');
  assert.equal(turns[0].messages[0].raw, 'short');
});
test('UNKNOWN has no blind retry or implicit new input', () => {
  assert.equal(canSubmit({ state: 'unknown' }, false), false);
  assert.equal(canSubmit({ state: 'unknown', ended: true }, false), true);
  assert.equal(canSubmit({ state: 'completed' }, false), true);
});
test('stream completion, interruption and stale deltas', () => {
  const event = { kind: 'model_started', run_id: 'r', attempt_id: 'a' };
  let stream = reduceStream(null, event);
  stream = reduceStream(stream, { ...event, kind: 'model_text', message: 'partial' });
  stream = reduceStream(stream, { ...event, kind: 'model_interrupted' });
  assert.equal(stream.raw, 'partial');
  assert.equal(stream.interrupted, true);
  assert.deepEqual(reduceStream(stream, { kind: 'model_text', attempt_id: 'old', message: 'bad' }), stream);
  assert.equal(reduceStream(stream, { ...event, kind: 'model_finished' }), null);
  assert.equal(reduceStream(null, { ...event, purpose: 'assessment' }), null);
});
test('denied storage does not break input', () => {
  const denied = { getItem() { throw new Error(); }, setItem() { throw new Error(); } };
  assert.equal(safeStorage(denied, 'draft'), '');
  assert.equal(safeStorage(denied, 'draft', 'text'), 'text');
});
test('long live streams have a finite display bound', () => {
  const stream = { attempt: 'a', raw: 'x'.repeat(1 << 20), interrupted: false };
  const next = reduceStream(stream, { kind: 'model_text', attempt_id: 'a', message: 'more' });
  assert.equal(next.raw.length, 1 << 20);
  assert.equal(next.truncated, true);
});
test('Provider failures use safe hints and cannot turn UNKNOWN into a retry', () => {
  for (const category of ['authentication', 'permission', 'quota', 'rate_limit', 'context_limit', 'capability',
    'usage', 'output_limit', 'refusal', 'content_filter', 'request', 'redirect', 'unavailable', 'timeout', 'cancelled', 'custom']) {
    const failure = { Certain: true, category, retryable: true, Message: '<secret>' };
    const known = providerFailureText(failure);
    assert.ok(known.length);
    assert.equal(known.includes(failure.Message), false);
    assert.match(providerFailureText({ ...failure, Certain: false }), /结果未知.*不要直接重试/);
  }
  assert.match(providerFailureText({ Certain: true, category: 'output_limit' }), /片段不是最终回答/);
  assert.match(providerFailureText({ Certain: true, category: 'rate_limit', retryable: true }), /未自动重试/);
  assert.equal(providerFailureText(null), '');
});
test('chat titles come from the first non-blank line, limited to 80 code points', () => {
  assert.equal(chatTitle('  整理 README  \n第二行\r\n第三行'), '整理 README');
  assert.equal(chatTitle('line one\rline two'), 'line one');
  assert.equal(chatTitle(' \t\n  \r\n'), '');
  assert.equal(chatTitle(''), '');
  assert.equal(chatTitle('\n\n   \n  第一段内容\n后续'), '第一段内容');
  const long = chatTitle('题'.repeat(100));
  assert.equal(Array.from(long).length, 80);
  assert.equal(long, '题'.repeat(80));
  const emoji = chatTitle('😀'.repeat(81));
  assert.equal(Array.from(emoji).length, 80);
  assert.equal(emoji.length, 160);
  assert.equal(chatTitle(`${'a'.repeat(79)}😀😀`), `${'a'.repeat(79)}😀`);
});
test('U1 state metadata and activity labels cover known and unknown values', () => {
  for (const state of ['generating', 'awaiting_approval', 'stopped', 'needs_attention', 'completed', 'failed', 'unknown']) {
    const meta = stateMeta(state);
    assert.equal(meta.label.length > 0, true);
    assert.equal(meta.icon.length > 0, true);
    assert.equal(meta.token.length > 0, true);
  }
  assert.equal(stateMeta('future_state').token, 'warn');
  const names = {
    command_run: ['运行命令', 'terminal'],
    http_get: ['请求网址', 'globe'],
    file_read: ['读取文件', 'file-text'],
    file_list: ['列出目录', 'folder-tree'],
    file_search: ['搜索文件', 'file-search'],
    file_edit: ['修改文件', 'file-pen'],
    file_create: ['创建文件', 'file-plus'],
    file_delete: ['删除文件', 'trash-2'],
    file_batch_edit: ['批量修改文件', 'files'],
    memory_list: ['列出记忆', 'brain'],
    memory_read: ['读取记忆', 'book-open'],
    memory_write: ['写入记忆', 'brain'],
    knowledge_search: ['检索知识', 'search'],
    model: ['模型生成', 'bot'],
    assessment: ['模型生成', 'bot'],
    planning: ['模型生成', 'bot'],
  };
  for (const [name, expected] of Object.entries(names)) assert.deepEqual(activityLabel(name), { label: expected[0], icon: expected[1] });
  assert.deepEqual(activityLabel('mcp_fetch'), { label: '调用工具', icon: 'plug-zap' });
  assert.deepEqual(activityLabel('other'), { label: 'other', icon: 'plug-zap' });
});
test('U1 submit mode and end risk preserve the unknown boundary', () => {
  assert.equal(submitMode(null, false), 'send');
  assert.equal(submitMode({ ended: true, state: 'completed' }, false), 'send');
  assert.equal(submitMode({ can_end: true, state: 'completed' }, true), 'blocked');
  assert.equal(submitMode({ can_end: false, state: 'generating' }, false), 'blocked');
  assert.equal(submitMode({ can_end: true, state: 'stopped' }, false), 'end_and_send');
  assert.equal(endRisk(null), 'low');
  assert.equal(endRisk({ activity: [], evidence: {} }), 'low');
  assert.equal(endRisk({ activity: [{ name: 'file_read', status: 'finished' }], evidence: {} }), 'low');
  assert.equal(endRisk({ activity: [{ kind: 'tool', name: 'file_edit', status: 'finished' }], evidence: {} }), 'medium');
  assert.equal(endRisk({ activity: [], evidence: { commands_run: [{ command: 'echo hi' }] } }), 'medium');
  assert.equal(endRisk({ state: 'awaiting_approval', activity: [], evidence: {} }), 'medium');
  assert.equal(endRisk({ state: 'unknown', activity: [], evidence: {} }), 'high');
  assert.equal(endRisk({ activity: [{ kind: 'tool', name: 'file_read', status: 'dispatched' }], evidence: {} }), 'high');
});
test('U1 evidence groups omit empty groups and shorten hashes', () => {
  const evidence = {
    files: [{ path: 'a.txt' }],
    commands_run: [],
    http_requests: [{ url: 'http://127.0.0.1' }],
    checks_passed: ['pnpm test'],
    checks_not_run: ['screenshots'],
    remaining_uncertainty: ['clipboard'],
  };
  assert.deepEqual(evidenceGroups(evidence).map((group) => group.key), ['files', 'http', 'checks', 'uncertainty']);
  assert.equal(shortHash('1234567890abcdef'), '12345678');
  assert.equal(shortHash('short'), 'short');
  assert.equal(shortHash(null), '');
});
test('turn signatures ignore clocks but track visible content and runtime changes', () => {
  const base = {
    key: 'run-1', state: 'generating', reason: '', can_continue: false, can_end: true,
    ended: false, elapsed_ms: 100, steps: 1,
    messages: [{ key: 'message-1', raw: 'hello', truncated: false }],
    activity: [{ key: 'activity-1', status: 'dispatched', text: 'progress' }],
    evidence: { files: [{ path: 'a' }] }, sources: [{ title: 'source' }],
  };
  assert.equal(turnSignature(base), turnSignature({ ...base, elapsed_ms: 999, steps: 9 }));
  assert.notEqual(turnSignature(base), turnSignature({ ...base, messages: [{ ...base.messages[0], raw: 'hello!' }] }));
  assert.notEqual(turnSignature(base), turnSignature({ ...base, messages: [{ ...base.messages[0], truncated: true }] }));
  assert.notEqual(turnSignature(base), turnSignature({ ...base, activity: [{ ...base.activity[0], status: 'finished' }] }));
  assert.notEqual(turnSignature(base), turnSignature({ ...base, evidence: { files: [{ path: 'a' }, { path: 'b' }] } }));
  assert.notEqual(turnSignature(base), turnSignature({ ...base, sources: [] }));
});
test('chat grouping uses local day boundaries and keeps provider order', () => {
  const now = new Date(2026, 9, 11, 15, 30);
  const chats = [
    { id: 'today', title: 'Alpha', updated_at: new Date(2026, 9, 11, 1).toISOString() },
    { id: 'yesterday', title: 'Beta', updated_at: new Date(2026, 9, 10, 23).toISOString() },
    { id: 'recent', title: 'Gamma', updated_at: new Date(2026, 9, 6, 23).toISOString() },
    { id: 'old', title: 'Delta', updated_at: new Date(2026, 8, 1).toISOString() },
  ];
  assert.deepEqual(groupChats(chats, now).map((group) => [group.label, group.items.map((item) => item.id)]), [
    ['今天', ['today']], ['昨天', ['yesterday']], ['近 7 天', ['recent']], ['更早', ['old']],
  ]);
  assert.equal(matchChat({ title: 'Ａlpha' }, 'alpha'), true);
  assert.equal(matchChat({ title: 'Alpha' }, 'beta'), false);
  assert.equal(attentionTitle('FreeAgent Chat', true), '（等待确认）FreeAgent Chat');
  assert.equal(attentionTitle('FreeAgent Chat', false), 'FreeAgent Chat');
});

test('pinned chats have a stable group and stay first in search results', () => {
  const chats = [
    { id: 'normal', title: 'Normal', updated_at: '2026-10-11T10:00:00Z' },
    { id: 'pinned', title: 'Pinned', pinned: true, pinned_at: '2026-10-11T09:00:00Z', updated_at: '2026-10-01T10:00:00Z' },
  ];
  assert.deepEqual(groupChats(chats, new Date('2026-10-11T12:00:00Z')).map((group) => [group.label, group.items.map((item) => item.id)]), [
    ['置顶', ['pinned']], ['今天', ['normal']],
  ]);
  assert.deepEqual(prioritizePinned(chats).map((item) => item.id), ['pinned', 'normal']);
});

test('permission mode follows an existing conversation snapshot and pending turn override', () => {
  assert.equal(permissionMode(null), 'ask');
  assert.equal(permissionMode({ permission: { mode: 'workspace' } }), 'workspace');
  assert.equal(permissionMode({ permission: { mode: 'full' } }), 'full');
  assert.equal(permissionMode({ permission: { mode: 'full' } }, 'workspace'), 'workspace');
});
