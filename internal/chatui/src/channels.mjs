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
      <header><div><h3>${label}</h3><p>与本机共用对话和权限；${source === 'telegram' ? '用户发送 /start 申请，管理员批准后仅获得聊天资格。' : '新配对默认为 None。'}</p></div><span class="permission-state ${view.running ? 'enabled' : ''}">${view.running ? '运行中' : config.enabled ? '连接未就绪' : '已关闭'}</span></header>
      <label class="check-line"><input type="checkbox" id="channel-${source}-enabled" ${config.enabled ? 'checked' : ''}>启用</label>
      <label>已保存的渠道 Key<select id="channel-${source}-key"><option value="">选择 Key 管理中的 ${label} 凭据</option>${keys.map(profile => `<option value="${esc(profile.id)}" ${config.key === profile.id ? 'selected' : ''}>${esc(profile.name)}</option>`).join('')}</select></label>
      ${source !== 'telegram' ? `<label>官方应用 App ID<input id="channel-${source}-app" value="${esc(config.app_id || '')}" autocomplete="off"></label>` : ''}
      ${source === 'qq' ? `<label>本机回调地址<input id="channel-qq-listen" value="${esc(config.listen || '127.0.0.1:8098')}" autocomplete="off"></label><p>将自己的公网 HTTPS 回调反向代理至此地址的 <code>/qq/events</code>。</p>` : ''}
      <p class="subtle-note">${view.bot_username ? `Bot：@${esc(view.bot_username)} ` : ''}${view.last_error ? `连接错误：${esc(view.last_error)}` : ''}</p>
      ${limitations.map(text => `<p class="subtle-note">${esc(text)}</p>`).join('')}
      ${source !== 'telegram' && view.pair_code ? `<p class="permission-notice">配对：<code>/pair ${esc(view.pair_code)}</code>；有效期至 ${esc(view.pair_expires_at || '')}</p>` : ''}
      <div class="permission-actions"><button class="primary" data-action="channel-save" data-source="${source}">保存连接配置</button>${source !== 'telegram' ? `<button data-action="channel-code" data-source="${source}" ${!view.running ? 'disabled' : ''}>生成 10 分钟配对码</button>` : '<button data-action="telegram-access-refresh">刷新接入申请</button>'}</div>
      ${source === 'telegram' ? telegramAccessSection(status.telegram_access || {}) : ''}
    </section>`;
  }).join('');
}

export function telegramAccessSection(state = {}) {
  return `<fieldset class="channel-access-controls" aria-label="Telegram 接入管理" ${state.busy ? 'disabled' : ''}>${state.busy ? '<p role="status" data-saving>正在保存接入设置…</p>' : ''}<h4>待批准申请</h4><p>核对申请码与数字身份。申请码不能兑换权限；批准不提升工具权限。</p>
    ${(state.requests || []).map((r,i) => `<div class="permission-section"><strong>${esc(r.code)}</strong> · ${esc(r.display_name || '')}<p>Bot ${esc(r.target?.application_id)} · ${esc(r.kind)} ${esc(r.target?.chat_id)} · ${esc(r.person_id)}</p><p class="subtle-note">到期 ${esc(r.expires_at)}</p>${r.kind === 'group' ? `<label class="check-line"><input type="checkbox" id="tg-request-whole-${i}">允许该群所有成员聊天（会消耗实例模型额度；默认仅申请人）</label>` : ''}<button class="primary" data-action="telegram-access-approve" data-index="${i}">批准此身份聊天</button></div>`).join('') || '<p class="subtle-note">暂无有效申请。Owner 重启或更换 Bot 后需重新申请。</p>'}
    <h4>接入名单与已有空间</h4>${(state.access || []).map((g,i) => `<div class="permission-section"><p>${esc(g.kind)} ${esc(g.target?.chat_id)} · ${g.enabled ? '已允许' : '尚未授权 / 已停用'} · 空间权限 ${esc(g.space_permission || 'none')}</p>${(g.people || []).map(p => `<p>${esc(p.id)}：人员 ${esc(p.mode)} → 最终 ${esc(g.effective_permissions?.[p.id] || 'none')}</p>`).join('')}
      <label>允许的人员 ID（telegram:user:数字，逗号分隔）<input id="tg-members-${i}" value="${esc((g.members?.length ? g.members : g.verified_people?.length === 1 ? g.verified_people : []).join(', '))}" autocomplete="off"></label>
      ${g.kind === 'group' ? `<label class="check-line"><input id="tg-whole-${i}" type="checkbox" ${g.whole_group ? 'checked' : ''}>群内所有成员可聊天（不授予工具）</label>` : ''}
      <p class="subtle-note">缺失或歧义身份请重新 /start。停用保留空间和历史；修改人员工具权限会影响其在其他空间的能力。</p><div class="permission-actions"><button data-action="telegram-access-save" data-index="${i}">允许 / 保存名单</button><button data-action="telegram-access-revoke" data-index="${i}" ${g.enabled ? '' : 'disabled'}>停用访问</button><button data-action="telegram-access-tools" data-index="${i}">调整空间与人员工具权限</button></div></div>`).join('') || '<p class="subtle-note">还没有 Telegram 空间。</p>'}
    <details><summary>直接添加已知数字身份</summary><label>聊天数字 ID<input id="tg-manual-chat" autocomplete="off"></label><label>类型<select id="tg-manual-kind"><option value="private">私聊</option><option value="group">群（指定成员）</option></select></label><label>用户数字 ID（逗号分隔）<input id="tg-manual-members" autocomplete="off"></label><button data-action="telegram-access-manual">核对并允许聊天</button></details></fieldset>`;
}
