import test from 'node:test';
import assert from 'node:assert/strict';
import { mergeCurrentTurns } from '../src/state.mjs';
import { summaryByteLimit, contextActions, contextStatus } from '../src/context.mjs';

test('completed maintenance disappears from current snapshots without dropping older history or UNKNOWN', () => {
  const old = { key: 'old', state: 'completed' };
  const transient = { key: 'summary', maintenance: true, state: 'generating' };
  const unknown = { key: 'unknown-summary', maintenance: true, state: 'unknown' };
  const latest = { key: 'latest', state: 'completed' };
  assert.deepEqual(mergeCurrentTurns([old, transient, latest], [latest]), [old, latest]);
  assert.deepEqual(mergeCurrentTurns([old, transient], [latest, unknown]), [old, latest, unknown]);
});

test('manual compact remains available when automatic compaction is off', () => {
  assert.deepEqual(contextActions({ status: 'none', automatic: false }), { compact: true, refresh: false, clear: false });
  assert.deepEqual(contextActions({ status: 'current', automatic: false }), { compact: true, refresh: true, clear: true });
});

test('stale summaries require refresh or clear and busy/UNKNOWN disables changes', () => {
  assert.deepEqual(contextActions({ status: 'stale' }), { compact: false, refresh: true, clear: true });
  for (const state of [null, { status: 'current', blocked: 'UNKNOWN' }]) {
    assert.deepEqual(contextActions(state), { compact: false, refresh: false, clear: false });
  }
  assert.deepEqual(contextActions({ status: 'current' }, true), { compact: false, refresh: false, clear: false });
});

test('summary byte bounds reject invalid numbers and status counts UTF-8 bytes', () => {
  assert.equal(summaryByteLimit(' 4096 '), 4096);
  for (const value of ['', '1.5', 'Infinity', 0, -1, 16385]) assert.throws(() => summaryByteLimit(value));
  assert.equal(contextStatus({ status: 'current', summary: { source_count: 4, text: '摘要' } }), '已覆盖 4 条记录 · 摘要 6 字节');
});
