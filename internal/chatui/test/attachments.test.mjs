import test from 'node:test';
import assert from 'node:assert/strict';
import { attachmentDraft, uploadProblem, attachmentCards } from '../frontend/lib/presentation/attachments.mjs';

test('attachment drafts reject unsafe or broken stored references', () => {
  assert.deepEqual(attachmentDraft('{broken'), []);
  assert.deepEqual(attachmentDraft([{ id: '../../secret', name: 'x', bytes: 1 }]), []);
  assert.equal(attachmentDraft([{ id: 'a'.repeat(32), name: '中文 & !.txt', bytes: 0 }]).length, 1);
});
test('batch limits include already uploaded files and permit empty files', () => {
  assert.equal(uploadProblem([{ size: 0 }]), '');
  assert.match(uploadProblem([{ size: 9 * 1024 ** 2 }]), /单个/);
  assert.match(uploadProblem([{ size: 1 }], Array(8).fill({ bytes: 0 })), /8/);
  assert.match(uploadProblem([{ size: 8 }], [{ bytes: 9 }], { max_input_bytes: 8, max_batch_bytes: 16 }), /总大小/);
});
test('attachment cards escape names and bind download to the original chat', () => {
  const html = attachmentCards([{ id: 'a'.repeat(32), name: '<script>x</script>', mime: 'text/html', bytes: 2 }], 'original');
  assert.ok(!html.includes('<script>'));
  assert.match(html, /data-attachment-chat="original"/);
  assert.ok(!html.includes('data-attachment-preview'));
});
