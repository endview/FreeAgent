import test from 'node:test';
import assert from 'node:assert/strict';
import { allowedExternalURL, desktopMode, editableTarget, menuAvailability, openDesktopExternal } from '../src/desktop.mjs';

function target({ tag = 'input', type = 'text', value = '', start = 0, end = 0, link = null } = {}) {
  return {
    type,
    value,
    selectionStart: start,
    selectionEnd: end,
    matches(selector) {
      return selector === 'input' ? tag === 'input' : selector === 'textarea' ? tag === 'textarea' : false;
    },
    closest(selector) {
      return selector === 'a[href]' ? link : null;
    },
  };
}

test('external URLs allow only safe web and mail protocols', () => {
  for (const value of [
    'http://example.test/path',
    'https://example.test/path',
    'mailto:person@example.test',
  ]) assert.equal(allowedExternalURL(value), true, value);
  for (const value of [
    'javascript:alert(1)',
    'data:text/plain,hello',
    'ftp://example.test/file',
    'https://user:password@example.test/',
    'https://',
    'mailto:',
  ]) assert.equal(allowedExternalURL(value), false, value);
});

test('desktop mode and external opening are opt-in', () => {
  assert.equal(desktopMode({ faOpenExternal() {} }), true);
  assert.equal(desktopMode({}), false);
  assert.equal(openDesktopExternal('https://example.test'), false);
});

test('editable menu actions distinguish selection and link targets', () => {
  const input = target({ value: 'hello', start: 1, end: 4 });
  assert.equal(editableTarget(input), input);
  assert.deepEqual(menuAvailability(input), {
    cut: true, copy: true, paste: true, selectAll: true, copyLink: false,
  });

  const noSelection = target({ value: 'hello' });
  assert.deepEqual(menuAvailability(noSelection, 'selected page text'), {
    cut: false, copy: true, paste: true, selectAll: true, copyLink: false,
  });

  const link = { href: 'https://example.test' };
  const messageLink = target({ tag: 'span', link });
  assert.equal(menuAvailability(messageLink).copyLink, true);
});

test('password fields never expose cut or copy actions', () => {
  const password = target({ type: 'password', value: 'secret', start: 0, end: 6 });
  assert.equal(editableTarget(password), null);
  assert.deepEqual(menuAvailability(password, 'secret'), {
    cut: false, copy: false, paste: false, selectAll: true, copyLink: false,
  });
});

test('pure menu checks tolerate missing DOM globals', () => {
  assert.deepEqual(menuAvailability(null), {
    cut: false, copy: false, paste: false, selectAll: true, copyLink: false,
  });
  assert.equal(allowedExternalURL('/relative'), true);
});
