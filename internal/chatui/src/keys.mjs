const statusLabels = {
  available: '可用',
  untested: '未测试',
  failed: '连接失败',
  reentry: '需要重新输入',
  env: '环境变量',
  none: '无需 Key',
  plaintext: '未加密',
};

export function keyHost(baseURL) {
  try {
    return new URL(baseURL).host || baseURL;
  } catch {
    return baseURL || '';
  }
}

export function keyStatus(profile) {
  if (profile?.needs_reentry) return { kind: 'reentry', label: statusLabels.reentry, icon: 'triangle-alert' };
  if (profile?.source === 'env') return { kind: 'env', label: statusLabels.env, icon: 'key-round' };
  if (profile?.source === 'none') return { kind: 'none', label: statusLabels.none, icon: 'plug-zap' };
  if (profile?.last_check?.result === 'ok') return { kind: 'available', label: statusLabels.available, icon: 'circle-check' };
  if (profile?.last_check) return { kind: 'failed', label: statusLabels.failed, icon: 'circle-x' };
  if (profile?.protection === 'plaintext') return { kind: 'plaintext', label: statusLabels.plaintext, icon: 'triangle-alert' };
  return { kind: 'untested', label: statusLabels.untested, icon: 'circle-question-mark' };
}

export function modelDisplay(id, model, profiles = []) {
  const profile = profiles.find((item) => item.id === model?.key);
  const name = model?.provider === 'echo' ? '本地 Echo' : model?.model || id;
  if (model?.missing) return `${name} · Key 已删除`;
  return `${name} · ${profile?.name || (model?.key ? 'Key 未找到' : '本地')}`;
}

export function modelsForKey(environment, keyID) {
  return Object.entries(environment?.models || {})
    .filter(([, model]) => model.key === keyID)
    .map(([id, model]) => ({ id, ...model }));
}

export function hasModels(environment) {
  return Object.keys(environment?.models || {}).length > 0;
}

export function keyFormValue(profile) {
  return {
    name: profile?.name || '',
    base_url: profile?.base_url || '',
    protocol: profile?.protocol || 'openai-compatible',
    note: profile?.note || '',
    source: profile?.source || 'saved',
  };
}
