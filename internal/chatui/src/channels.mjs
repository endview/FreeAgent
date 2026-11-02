import { escapeHTML as esc } from './render.mjs';

export const CHANNELS = [ ['telegram', 'Telegram'], ['lark', 'Lark / 飞书'], ['qq', 'QQ 官方机器人'] ];
export function channelConfig(status, source) {
  return { ...(status?.config?.[source] || status?.[source]?.config || {}), enabled: Boolean(status?.config?.[source]?.enabled ?? status?.[source]?.config?.enabled ?? status?.[source]?.enabled) };
}
export function channelSections(status = {}, profiles = []) {
  return CHANNELS.map(([source, label]) => {
    const view = status[source] || {}, config = channelConfig(status, source);
    const keys = profiles.filter(profile => profile.protocol === source);
    const limitations = view.limitations || (source === 'telegram' ? ['群背景只覆盖 Bot 实际收到的文字；请核对 Privacy Mode。'] : []);
    return `<section class="permission-section" data-channel="${source}">
      <header><div><h3>${label}</h3><p>与本机共用对话和权限；新配对默认为 None。</p></div><span class="permission-state ${view.running ? 'enabled' : ''}">${view.running ? '运行中' : config.enabled ? '连接未就绪' : '已关闭'}</span></header>
      <label class="check-line"><input type="checkbox" id="channel-${source}-enabled" ${config.enabled ? 'checked' : ''}>启用</label>
      <label>已保存的渠道 Key<select id="channel-${source}-key"><option value="">选择 Key 管理中的 ${label} 凭据</option>${keys.map(profile => `<option value="${esc(profile.id)}" ${config.key === profile.id ? 'selected' : ''}>${esc(profile.name)}</option>`).join('')}</select></label>
      ${source !== 'telegram' ? `<label>官方应用 App ID<input id="channel-${source}-app" value="${esc(config.app_id || '')}" autocomplete="off"></label>` : ''}
      ${source === 'qq' ? `<label>本机回调地址<input id="channel-qq-listen" value="${esc(config.listen || '127.0.0.1:8098')}" autocomplete="off"></label><p>将自己的公网 HTTPS 回调反向代理至此地址的 <code>/qq/events</code>。</p>` : ''}
      <p class="subtle-note">${view.bot_username ? `Bot：@${esc(view.bot_username)} ` : ''}${view.last_error ? `连接错误：${esc(view.last_error)}` : ''}</p>
      ${limitations.map(text => `<p class="subtle-note">${esc(text)}</p>`).join('')}
      ${view.pair_code ? `<p class="permission-notice">配对：<code>/pair ${esc(view.pair_code)}</code>；有效期至 ${esc(view.pair_expires_at || '')}</p>` : ''}
      <div class="permission-actions"><button class="primary" data-action="channel-save" data-source="${source}">保存连接配置</button><button data-action="channel-code" data-source="${source}" ${!view.running ? 'disabled' : ''}>生成 10 分钟配对码</button></div>
    </section>`;
  }).join('');
}
