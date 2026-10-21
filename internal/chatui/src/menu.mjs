export function nextIndex(items, from, step) {
  if (!items.length) return -1;
  let index = Number.isInteger(from) ? from : (step > 0 ? -1 : items.length);
  for (let count = 0; count < items.length; count++) {
    index = (index + step + items.length) % items.length;
    if (!items[index].disabled) return index;
  }
  return -1;
}

export function modelMenuItems(environment, profiles = []) {
  const items = [];
  if (environment?.auto) items.push({ id: 'auto', label: 'Auto', kind: 'model', disabled: false });
  for (const [id, model] of Object.entries(environment?.models || {})) {
    const profile = profiles.find((item) => item.id === model.key);
    const name = model.provider === 'echo' ? '本地 Echo' : model.model || id;
    items.push({
      id, kind: 'model', disabled: Boolean(model.missing),
      label: `${name} · ${model.missing ? 'Key 已删除' : profile?.name || (model.key ? 'Key 未找到' : '本地')}`,
    });
  }
  items.push({ id: 'add-key', kind: 'action', disabled: false, label: '添加 Key' });
  items.push({ id: 'manage-keys', kind: 'action', disabled: false, label: 'Key 管理' });
  return items;
}
