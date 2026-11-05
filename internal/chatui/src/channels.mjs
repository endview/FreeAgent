import { escapeHTML as esc } from './render.mjs';

export const CHANNELS = [ ['telegram', 'Telegram'], ['lark', 'Lark / 飞书'], ['qq', 'QQ 官方机器人'] ];
export function channelConfig(status,source) {
  return {...(status?.config?.connections?.find(c=>c.source===source)||{} )};
}
export function channelSections(status={},profiles=[]) {
  const connections=status.config?.connections||[];
  const states=status.connections||[];
  return `<section class="permission-section" data-settings-section="channels"><h3>可选渠道进程</h3><p>默认不连接任何平台。每个账号明确绑定一个 Core；渠道只传输消息，权限和执行仍由 Core 处理。</p>
  ${connections.map(c=>{const view=states.find(s=>s.id===c.id);return `<div class="channel-connection"><strong>${esc(c.id)}</strong><span>${esc(c.source)} · ${esc(c.application_id)} · ${view?.running?'在线':c.enabled?'等待进程连接':'已关闭'}</span><button data-action="channel-save" data-id="${esc(c.id)}">${c.enabled?'关闭':'启用'}</button><button data-action="channel-connection-delete" data-id="${esc(c.id)}">删除连接</button></div>`}).join('')||'<p>尚未配置渠道。</p>'}
  <details><summary>添加渠道账号</summary><label>连接 ID<input id="channel-id" autocomplete="off" placeholder="telegram-main"></label><label>平台<select id="channel-source">${CHANNELS.map(([id,label])=>`<option value="${id}">${label}</option>`).join('')}</select></label>
  <label>Bot 数字 ID / 应用 ID<input id="channel-app" autocomplete="off"></label><p>Lark 使用 app:tenant；QQ 使用官方应用 ID。</p><label>渠道凭据<select id="channel-key"><option value="">先保存下方渠道凭据</option>${profiles.map(k=>`<option value="${esc(k.id)}">${esc(k.name)} · ${esc(k.protocol)}</option>`).join('')}</select></label>
  <label>QQ 本机回调（其他平台留空）<input id="channel-listen" autocomplete="off" placeholder="127.0.0.1:8098"></label><p>QQ 通过反向代理使用本机 <code>/qq/events</code>。</p><label><input id="channel-enabled" type="checkbox">保存后启用（会连接平台）</label><button data-action="channel-add">保存连接</button></details>
  </section>
  <section class="permission-section" data-settings-section="channels"><h3>渠道凭据</h3><p>与模型 Provider 独立保存。Secret 不进入模型上下文。</p>${profiles.map(k=>`<div class="channel-connection"><span>${esc(k.name)} · ${esc(k.protocol)}</span><button data-action="channel-credential-delete" data-id="${esc(k.id)}">删除凭据</button></div>`).join('')}
  <form id="channel-credential-form"><label>名称<input id="channel-credential-name" required maxlength="64"></label><label>平台<select id="channel-credential-protocol">${CHANNELS.map(([id,label])=>`<option value="${id}">${label}</option>`).join('')}</select></label><label>平台 API 地址<input id="channel-credential-origin" required placeholder="https://api.telegram.org"></label><label>Bot Token / 应用 Secret<input id="channel-credential-secret" type="password" autocomplete="new-password" required></label><button type="button" data-action="channel-credential-save">保存渠道凭据</button></form></section>
  ${connections.some(c=>c.source==='telegram'&&c.enabled)?`<section class="permission-section" data-settings-section="channels"><h3>Telegram 准入</h3>${telegramAccessSection(status.telegram_access||{})}</section>`:''}`;
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
