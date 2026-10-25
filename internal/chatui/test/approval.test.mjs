import test from 'node:test';
import assert from 'node:assert/strict';

test('patch approval retains both exact hashes and line counts', () => {
  const html=approvalHTML({tool:'apply_patch',items:[{kind:'file_edit',target:'exact.txt',old:'before',new:'after',fields:{before_sha256:'a'.repeat(64),after_sha256:'b'.repeat(64),old_lines:'1',new_lines:'1'}}]});
  assert.ok(html.includes('a'.repeat(64))&&html.includes('b'.repeat(64))&&html.includes('new_lines'));
});
import { diffLines, approvalItemsHTML, approvalHTML, formatControlCode } from '../src/approval.mjs';

test('line diff marks removals, additions and unchanged lines', () => {
  const result = diffLines('one\ntwo\nthree', 'one\nnew\nthree');
  assert.equal(result.bounded, false);
  assert.deepEqual(result.lines, [
    { kind: 'same', text: 'one' },
    { kind: 'remove', text: 'two' },
    { kind: 'add', text: 'new' },
    { kind: 'same', text: 'three' },
  ]);
});

test('large diff remains bounded and shows both full blocks', () => {
  const oldText = Array.from({ length: 2001 }, (_, index) => `old-${index}`).join('\n');
  const newText = Array.from({ length: 2001 }, (_, index) => `new-${index}`).join('\n');
  const result = diffLines(oldText, newText);
  assert.equal(result.bounded, true);
  assert.equal(result.old, oldText);
  assert.equal(result.new, newText);
});

test('approval rendering includes non-sandbox warnings and structured fields', () => {
  const items = [
    { kind: 'command', target: 'node', fields: { command: 'node script.js', timeout_s: '10' } },
    { kind: 'http', target: 'https://example.com', fields: { url: 'https://example.com' } },
    { kind: 'mcp', target: 'server/tool', fields: { arguments: '{"ok":true}' } },
  ];
  const html = approvalItemsHTML(items);
  assert.equal((html.match(/非沙箱/g) || []).length, 3);
  assert.match(html, /node script\.js/);
  assert.match(approvalHTML({ tool: 'command_run', preview: 'raw', items }), /批准执行/);
});

test('approval card does not autofocus its approval action', () => {
  const html = approvalHTML({ tool: 'file_edit', preview: 'raw', items: [
    { kind: 'file_edit', target: 'note.txt', sha256: '1234567890', old: 'old', new: 'new' },
  ] });
  assert.match(html, /<header tabindex="-1">/);
  assert.doesNotMatch(html, /autofocus/);
  assert.match(html, /diff-remove/);
  assert.match(html, /diff-add/);
});

const controlProposal = {
  tool: 'control_plan_change', preview: 'Change execution.set_permission for future Runs from "ask" to "full".',
  control_operation_id: 'op-1',
  items: [{ kind: 'control_proposal', target: 'op-1', fields: { kind: 'execution.set_permission', risk: 'authority', status: 'pending' } }],
};

test('control proposal card shows the owner code and enables apply', () => {
  const html = approvalHTML(controlProposal, { control: { operation_id: 'op-1', code: 'abcde23456', expires_at: '2026-10-19T10:00:00Z' } });
  assert.match(html, /ABCDE-23456/);
  assert.match(html, /应用变更/);
  assert.match(html, /扩大权限/);
  assert.match(html, /execution\.set_permission/);
  assert.doesNotMatch(html, /control_proposal/);
  assert.doesNotMatch(html, /data-action="approve" disabled/);
});

test('control proposal card never shows a code bound to another operation', () => {
  const html = approvalHTML(controlProposal, { control: { operation_id: 'other', code: 'ZZZZZ99999' } });
  assert.doesNotMatch(html, /ZZZZZ/);
  assert.match(html, /data-action="approve" disabled/);
  assert.match(approvalHTML(controlProposal), /data-action="approve" disabled/);
});

test('control code formatting ignores case, spaces and separators', () => {
  assert.equal(formatControlCode('abcde 23-456'), 'ABCDE-23456');
  assert.equal(formatControlCode(''), '');
});
