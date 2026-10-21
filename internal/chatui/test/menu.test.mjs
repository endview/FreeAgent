import test from 'node:test';
import assert from 'node:assert/strict';
import { nextIndex, modelMenuItems } from '../src/menu.mjs';

test('nextIndex skips disabled items and wraps', () => {
  const items = [{ disabled: false }, { disabled: true }, { disabled: false }];
  assert.equal(nextIndex(items, 0, 1), 2);
  assert.equal(nextIndex(items, 2, 1), 0);
  assert.equal(nextIndex(items, 0, -1), 2);
  assert.equal(nextIndex([{ disabled: true }], 0, 1), -1);
});
test('model menu keeps Auto first and management actions last', () => {
  const items = modelMenuItems({
    auto: true,
    models: {
      missing: { model: 'deepseek-flash', key: 'gone', missing: true },
      echo: { provider: 'echo' },
    },
  }, [{ id: 'gone', name: 'Deleted' }]);
  assert.equal(items[0].id, 'auto');
  assert.equal(items.find((item) => item.id === 'missing').disabled, true);
  assert.match(items.find((item) => item.id === 'missing').label, /Key 已删除/);
  assert.deepEqual(items.slice(-2).map((item) => item.label), ['添加 Key', 'Key 管理']);
});
