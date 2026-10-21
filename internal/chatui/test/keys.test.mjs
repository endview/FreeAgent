import test from 'node:test';
import assert from 'node:assert/strict';
import { hasModels, keyFormValue, keyHost, keyStatus, modelDisplay, modelsForKey } from '../src/keys.mjs';

test('key status prioritizes re-entry, then source and last check', () => {
  assert.equal(keyStatus({ needs_reentry: true }).kind, 'reentry');
  assert.equal(keyStatus({ source: 'env' }).kind, 'env');
  assert.equal(keyStatus({ source: 'none' }).kind, 'none');
  assert.equal(keyStatus({ source: 'saved', last_check: { result: 'ok' } }).kind, 'available');
  assert.equal(keyStatus({ source: 'saved', last_check: { result: 'auth' } }).kind, 'failed');
  assert.equal(keyStatus({ source: 'saved' }).kind, 'untested');
});

test('key hosts and forms stay safe for invalid or missing values', () => {
  assert.equal(keyHost('https://api.example.test/v1'), 'api.example.test');
  assert.equal(keyHost('not an address'), 'not an address');
  assert.deepEqual(keyFormValue(), { name: '', base_url: '', protocol: 'openai-compatible', note: '', source: 'saved' });
});

test('models display provider name and key name, including missing keys', () => {
  const profiles = [{ id: 'k-1', name: 'DeepSeek' }];
  assert.equal(modelDisplay('flash', { provider: 'openai-compatible', model: 'deepseek-flash', key: 'k-1' }, profiles), 'deepseek-flash · DeepSeek');
  assert.equal(modelDisplay('gone', { provider: 'openai-compatible', model: 'gone', key: 'k-missing', missing: true }, profiles), 'gone · Key 已删除');
  assert.equal(modelDisplay('echo', { provider: 'echo' }, profiles), '本地 Echo · 本地');
});

test('key model projection and zero-model state are deterministic', () => {
  const env = { models: {
    a: { provider: 'openai-compatible', model: 'a', key: 'k-1' },
    b: { provider: 'openai-compatible', model: 'b', key: 'k-2' },
  } };
  assert.deepEqual(modelsForKey(env, 'k-1').map((item) => item.id), ['a']);
  assert.equal(hasModels({ models: {} }), false);
  assert.equal(hasModels(env), true);
});
