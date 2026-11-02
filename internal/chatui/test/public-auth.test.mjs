import test from 'node:test';
import assert from 'node:assert/strict';
import { apiPath, authHeaders, publicSession, loginPublicPassword } from '../src/public-auth.mjs';

test('public transport cannot carry local capabilities or arbitrary attach routes', () => {
  assert.deepEqual(authHeaders({ remote: true, key: 'private-local', csrf: 'csrf', active: true }), { 'X-FreeAgent-CSRF': 'csrf', 'X-FreeAgent-Activity': '1' });
  assert.equal(apiPath('attach/context?conversation_id=c', true), '/api/remote/context?conversation_id=c');
  assert.equal(apiPath('attachments/opaque', true), '/api/attachments/opaque');
  for (const path of ['attach/state', 'attach/handoff', 'attach/backup', 'attach/submit']) assert.throws(() => apiPath(path, true));
  assert.equal(apiPath('attach/state', false), '/api/attach/state');
});

test('explicit login refreshes an expired one-use detection cookie before password submission', async () => {
  let validCookie = false, posts = 0;
  const result = await loginPublicPassword('fixture password', async (path, options) => {
    if (path === '/api/public/session') { validCookie = true; return { ok: true, json: async () => ({ authenticated: false }) }; }
    assert.equal(path, '/api/public/login'); assert.equal(validCookie, true); validCookie = false; posts++;
    assert.equal(JSON.parse(options.body).password, 'fixture password'); assert.equal(options.credentials, 'same-origin');
    return { ok: true, json: async () => ({ authenticated: true, csrf: 'new' }) };
  });
  assert.equal(result.csrf, 'new'); assert.equal(posts, 1);
  const existing = await loginPublicPassword('unused', async path => { assert.equal(path, '/api/public/session'); return { ok: true, json: async () => ({ authenticated: true, csrf: 'shared' }) }; });
  assert.equal(existing.csrf, 'shared');
});

test('public session discovery requests same-origin cookie and reports policy refusal', async () => {
  const session = await publicSession(async (path, options) => {
    assert.equal(path, '/api/public/session'); assert.equal(options.credentials, 'same-origin');
    assert.equal(options.headers, undefined);
    return { ok: true, json: async () => ({ authenticated: true, csrf: 'separate' }) };
  });
  assert.equal(session.csrf, 'separate');
  await assert.rejects(() => publicSession(async () => ({ ok: false })), /CLI/);
});
