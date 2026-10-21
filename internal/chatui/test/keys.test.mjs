import test from 'node:test';
import assert from 'node:assert/strict';
import { hasModels, keyDraftChanged, keyFormValue, keyHost, keyStatus, modelDisplay, modelsForKey } from '../src/keys.mjs';

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
  assert.deepEqual(keyFormValue(), { name: '', base_url: '', protocol: 'openai-compatible', note: '', source: 'saved', key: '' });
});

test('unsaved key drafts survive a re-render and override saved values', () => {
  const profile = { id: 'k-1', name: 'DeepSeek', base_url: 'https://api.deepseek.com', protocol: 'openai-compatible', note: '', source: 'saved' };
  const draft = { name: 'DeepSeek work', base_url: 'https://api.deepseek.com', protocol: 'openai-compatible', note: 'n', source: 'saved', key: 'secret' };
  assert.deepEqual(keyFormValue(profile, draft), { name: 'DeepSeek work', base_url: 'https://api.deepseek.com', protocol: 'openai-compatible', note: 'n', source: 'saved', key: 'secret' });
  assert.deepEqual(keyFormValue(null, { name: 'New', base_url: '', key: 'k' }), { name: 'New', base_url: '', protocol: 'openai-compatible', note: '', source: 'saved', key: 'k' });
  assert.equal(keyFormValue(profile, { name: '' }).name, '', 'a cleared field stays cleared');
});

test('a draft is kept only while the form differs from the saved profile', () => {
  const profile = { name: 'DeepSeek', base_url: 'https://api.deepseek.com', protocol: 'openai-compatible', note: '', source: 'saved' };
  const form = { name: 'DeepSeek', base_url: 'https://api.deepseek.com', protocol: 'openai-compatible', note: '', source: 'saved', key: '' };
  assert.equal(keyDraftChanged(profile, form), false);
  assert.equal(keyDraftChanged(profile, { ...form, key: 'secret' }), true);
  assert.equal(keyDraftChanged(profile, { ...form, note: 'x' }), true);
  assert.equal(keyDraftChanged({ ...profile, source: 'env' }, { ...form, source: '' }), false, 'env profiles report no source');
  assert.equal(keyDraftChanged(null, { name: '', base_url: '', protocol: 'openai-compatible', note: '', source: 'saved', key: '' }), false);
  assert.equal(keyDraftChanged(null, { name: 'x', base_url: '', protocol: 'openai-compatible', note: '', source: 'saved', key: '' }), true);
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
