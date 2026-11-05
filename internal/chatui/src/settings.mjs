import { escapeHTML as esc } from './render.mjs';

// UI routes only. Mutations continue through the existing Core control API.
export const SETTINGS = [
  ['models', '模型与 Provider', [['providers','服务与凭据','keys'],['models','可用模型与默认值','keys']]],
  ['capabilities','能力管理',[['tools','内置工具','settings'],['mcp','MCP 服务','capabilities'],['skills','Skills','capabilities'],['extensions','扩展','capabilities']]],
  ['execution','权限与执行',[['permissions','默认权限和文件范围','settings'],['commands','命令与网络','settings']]],
  ['context','上下文',[['window','窗口和压缩','settings'],['memory','Memory 与固定内容','settings']]],
  ['connections','连接与安全',[['public','公网 Web','settings'],['channels','渠道账号','settings']]],
  ['data','数据与运行',[['database','PostgreSQL','settings'],['diagnostics','备份与排障','settings']]],
];
export function settingsRoute(id) {
  for (const [group,label,pages] of SETTINGS) for (const [page,title,view] of pages) {
    if (page===id) return {id:page,group,label,title,view};
  }
  return settingsRoute('providers');
}
export function settingsNavigation(id,{channels=false}={}) {
  const route=settingsRoute(id);
  const group=SETTINGS.find(([name])=>name===route.group);
  const item=(id,label,selected)=>`<button type="button" data-action="settings-page" data-page="${esc(id)}" ${selected?'aria-current="page"':''}>${esc(label)}</button>`;
  const pages=group[2].filter(([page])=>page!=='channels'||channels||page===id);
  return `<div class="settings-groups" aria-label="设置分类">${SETTINGS.map(([name,label,pages])=>item(pages[0][0],label,name===route.group)).join('')}</div><div class="settings-subpages" aria-label="${esc(route.label)}">${pages.map(([id,label])=>item(id,label,id===route.id)).join('')}</div>`;
}

// Volatile form drafts: secrets stay in this page's memory and never enter
// browser storage, URLs, logs, or another settings page.
export class SettingsDrafts {
  #pages=new Map();
  remember(key,fields) {
    this.#pages.set(key,Array.from(fields).filter(f=>f.id&&f.type!=='file').map(f=>({id:f.id,value:f.value,checked:!!f.checked,type:f.type})));
  }
  restore(key,find) {
    for(const saved of this.#pages.get(key)||[]) {const field=find(saved.id);if(!field)continue;field.value=saved.value;if(saved.type==='checkbox'||saved.type==='radio')field.checked=saved.checked;}
  }
  discard(key){this.#pages.delete(key);}
  has(key){return this.#pages.has(key);}
}
