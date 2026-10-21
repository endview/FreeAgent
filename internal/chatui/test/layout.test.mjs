import test from 'node:test';
import assert from 'node:assert/strict';
import { clampLayout, readLayout, writeLayout, DEFAULTS, LIMITS, storageKey } from '../src/layout.mjs';

test('layout values stay within stable bounds', () => {
  assert.equal(clampLayout('sidebar', 100), LIMITS.sidebar[0]);
  assert.equal(clampLayout('sidebar', 999), LIMITS.sidebar[1]);
  assert.equal(clampLayout('column', 700, 640), 640);
  assert.equal(clampLayout('composer', 'bad'), DEFAULTS.composer);
});

test('layout preferences tolerate malformed storage and round-trip valid values', () => {
  const values = new Map();
  const storage = {
    getItem(key) { return values.get(key) || ''; },
    setItem(key, value) { values.set(key, value); },
  };
  assert.deepEqual(readLayout(storage), DEFAULTS);
  writeLayout(storage, { sidebar: 300, column: 900, composer: 180 });
  assert.deepEqual(readLayout(storage), { sidebar: 300, column: 900, composer: 180 });
  values.set(storageKey(), '{');
  assert.deepEqual(readLayout(storage), DEFAULTS);
  writeLayout(storage, { sidebar: 280, column: 700, composer: 120 }, 'env-2');
  assert.deepEqual(readLayout(storage, 'env-2'), { sidebar: 280, column: 700, composer: 120 });
  assert.deepEqual(readLayout(storage, 'env-1'), DEFAULTS);
});
