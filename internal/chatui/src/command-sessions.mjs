import { escapeHTML as esc } from './render.mjs';

export function commandState(record) {
  return ({ running: '运行中', stopping: '正在停止', completed: '已完成', failed: '失败',
    stopped: '已停止', timed_out: '期限已到', unknown: '结果未知' })[record.state] || record.state;
}
export function remainingSeconds(expires,now=Date.now()) {const end=Date.parse(expires);return Number.isFinite(end)?Math.max(0,Math.ceil((end-now)/1000)):0;}
function commandTitle(record) {
  try { const argv=JSON.parse(record.receipt?.command); if(Array.isArray(argv)&&argv.length) return [String(argv[0]).split(/[\\/]/).at(-1),...argv.slice(1)].join(' ').slice(0,160); } catch {}
  return record.receipt?.command || record.id;
}
export function commandRows(records, icon) {
  if (!records.length) return '<p class="subtle-note">没有后台命令</p>';
  return records.map((r) => `<article class="command-row" data-command-id="${esc(r.id)}">
    <div><strong title="${esc(r.receipt?.command || '')}">${esc(commandTitle(r))}</strong><span>${esc(commandState(r))} · ${r.mode==='service'?'服务':'后台命令'} · ${esc(r.expires_at)}</span>${r.mode==='service' ? `<span>${r.ready?'已观察到就绪文本':r.ready_text?'等待就绪文本':'未检查就绪'}${r.state==='running'?` · 剩余 <time data-service-expiry="${esc(r.expires_at)}">${remainingSeconds(r.expires_at)}</time> 秒`:''}</span>`:''}<code>${esc(r.id)}</code></div>
    <div class="command-actions"><button class="icon" data-action="command-poll" data-id="${esc(r.id)}" title="读取输出" aria-label="读取输出">${icon('scroll-text')}</button>${r.mode==='service'&&r.state==='running'?`<label class="command-extension">秒<input type="number" aria-label="延长秒数" min="1" max="28800" value="1800"></label><button class="icon" data-action="command-extend" data-id="${esc(r.id)}" data-revision="${r.revision}" data-expires="${esc(r.expires_at)}" title="延长服务" aria-label="延长服务">${icon('arrow-up')}</button>`:''}${['running', 'stopping'].includes(r.state) ? `<button class="icon" data-action="command-stop" data-id="${esc(r.id)}" title="停止命令" aria-label="停止命令">${icon('square')}</button>` : ''}</div>
  </article>`).join('');
}
export function commandOutput(record) {
  const cleanup=({confirmed:'已确认结束',owned:'由当前引擎持有',unknown:'未确认'})[record.receipt?.cleanup]||'未确认';
  return `${commandState(record)} · 进程树：${cleanup}\n${record.output_gap ? '[前段输出已丢弃或不完整]\n' : ''}${(record.output || []).map((part) => part.text).join('')}`;
}

export function directCommandText(raw, role) {
  try {
    const value=JSON.parse(role==='user'?raw.replace(/^Explicit command request: /,''):raw);
    if(role==='user'&&typeof value.command==='string') {
      const name=value.command.split(/[\\/]/).at(-1);
      return `${value.mode==='service'?'启动服务':value.background?'启动后台命令':value.check?'运行检查':'运行命令'}：${name}\n工作目录：${value.cwd || '.'}`;
    }
    if(role==='assistant'&&value.id&&value.receipt?.status==='started') return `${value.mode==='service'?'服务':'后台命令'}已启动\n命令 ID：${value.id}\n截止时间：${value.expires_at}`;
    if(role==='assistant'&&typeof value.output==='string'&&value.command) return value.output || `命令${value.command.exit_code===0?'已完成':'已结束'}，退出码：${value.command.exit_code ?? '未知'}`;
  } catch {}
  return raw;
}
