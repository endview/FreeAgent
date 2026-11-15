// Real isolated Core with explicit local-only channel/delivery display fixtures.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
const sections=[['general','通用'],['spaces','空间与会话'],['providers','模型与 Key'],['permissions','权限与执行'],['files','文件范围'],['commands','命令执行'],['sessions','后台命令'],['context','上下文'],['tools','内置工具'],['mcp','MCP'],['skills','Skill'],['memory','Memory 与固定内容'],['extensions','扩展'],['channels','渠道'],['delivery','投递授权'],['public','公网安全'],['data','数据库与备份'],['attachments','附件'],['diagnosis','诊断'],['advanced','高级策略']];
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS,audit=process.env.FREEAGENT_UI_AUDIT_ONLY==='1';assert(root&&out);fs.mkdirSync(out,{recursive:true});
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')),key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
 const headers={'X-FreeAgent-Key':key,Origin:info.origin,'Content-Type':'application/json'};
 const result=await fetch(info.origin+'/api/chats',{method:'POST',headers,body:JSON.stringify({title:'界面对齐检查 · 合成会话'})});assert(result.ok);const chat=await result.json();
 const browser=await chromium.launch({channel:process.env.FREEAGENT_BROWSER_CHANNEL||'chrome',headless:true}),page=await browser.newPage(),errors=[],reports=[],checks={};page.on('pageerror',e=>errors.push(e.message));
 const connection={id:'telegram-example',source:'telegram',application_id:'123456789',credential:'fixture-only',enabled:true,plain_text:false,disable_typing:false,reply_stickers:false,autonomous_reactions:false};let channelSaves=0;
 await page.route('**/api/environment',async route=>{const res=await route.fetch(),data=await res.json();await route.fulfill({response:res,json:{...data,delivery_available:true}});});
 await page.route('**/api/channels',route=>{if(route.request().method()==='PATCH'){const body=route.request().postDataJSON();Object.assign(connection,body.connections[0]);channelSaves++;}return route.fulfill({json:{config:{connections:[connection]},connections:[{id:connection.id,running:true}],expected_revisions:{}}});});
 await page.route('**/api/channels/telegram/access',route=>route.fulfill({json:{requests:[],access:[]}}));
 async function open(section,label){await page.goto(info.origin+'/?view=settings&section='+section+'&chat='+chat.id+'#key='+key);await page.locator('.settings-heading h1').filter({hasText:label}).waitFor();await page.locator('.loading-label').waitFor({state:'hidden'});}
 try{
  for(const [width,height] of (audit?[[2560,1080],[390,844]]:[[2560,1080],[1440,900],[1024,768],[768,1024],[390,844],[320,568],[844,390]])){
   await page.setViewportSize({width,height});await page.emulateMedia({colorScheme:width===1024||width===320?'dark':'light'});
   for(const [section,label] of sections){
    await open(section,label);
    if(section==='memory')await page.getByRole('button',{name:'读取当前对话 Memory',exact:true}).waitFor();
    if(section==='channels')await page.getByRole('checkbox',{name:'使用 Telegram 富文本',exact:true}).waitFor();
    if(section==='delivery'){await page.getByRole('button',{name:'新增跟进规则',exact:true}).click();await page.getByRole('textbox',{name:'规则标识',exact:true}).fill('example-followup');}
    const geometry=await page.locator('.settings-detail').evaluate(root=>{
     const title=root.querySelector('.settings-heading').getBoundingClientRect(),content=root.querySelector('.settings-content')||root;
     const nodes=[...content.children].filter(e=>e.getClientRects().length&&e.matches('p,form,article,details,.setting-field,.settings-section,.providers-layout,button:not(.settings-back),label'));
     const offsets=nodes.map(e=>({tag:e.tagName,kind:e.className,delta:Math.round(e.getBoundingClientRect().left-title.left)})).filter(e=>Math.abs(e.delta)>2);
     const overflow=[...root.querySelectorAll('input,textarea,select,[role=combobox],.setting-field')].filter(e=>e.getClientRects().length&&e.getBoundingClientRect().right>root.getBoundingClientRect().right+1).map(e=>e.getAttribute('aria-label')||e.tagName);
     const small=[...root.querySelectorAll('input[type=checkbox]')].filter(e=>e.getClientRects().length).filter(e=>{const r=e.getBoundingClientRect(),label=e.closest('label');return r.width<18||r.height<18||!label||parseFloat(getComputedStyle(label).fontSize)<14||label.getBoundingClientRect().height<44;}).map(e=>e.closest('label')?.textContent.trim().slice(0,32));
     return {offsets,overflow,small,rootOverflow:root.scrollWidth>root.clientWidth+1};
    });
    reports.push({section,width,height,...geometry});
    if(['memory','delivery','channels'].includes(section)&&[2560,1440,390,320].includes(width))await page.screenshot({path:path.join(out,`consistency-${section}-${width}.png`)});
   }
  }
  checks.all_settings_share_content_axis=reports.every(r=>!r.offsets.length);
  checks.no_horizontal_overflow=reports.every(r=>!r.overflow.length&&!r.rootOverflow);
  checks.checkbox_typography_and_hit_targets=reports.every(r=>!r.small.length);
  await page.setViewportSize({width:1440,height:900});await open('channels','渠道');
  const choice=page.getByRole('checkbox',{name:'使用 Telegram 富文本',exact:true});const saved=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/channels'&&r.request().method()==='PATCH');await choice.click();assert((await saved).ok());await page.waitForFunction(()=>{const el=[...document.querySelectorAll('label')].find(x=>x.textContent.includes('使用 Telegram 富文本'))?.querySelector('input');return el&&!el.checked&&!el.disabled;});await page.reload();await choice.waitFor();checks.channel_toggle_saves=channelSaves===1&&!await choice.isChecked();
  checks.no_runtime_errors=!errors.length;
  fs.writeFileSync(path.join(out,'consistency-results.json'),JSON.stringify({audit,checks,reports,errors,fixture:'Telegram connection is a local browser fixture; no platform requests.'},null,2));
  console.log(JSON.stringify({audit,checks,page_viewports:reports.length,failed_views:reports.filter(r=>r.offsets.length||r.overflow.length||r.small.length||r.rootOverflow).map(r=>({section:r.section,width:r.width,offsets:r.offsets,small:r.small,overflow:r.overflow})),errors}));
  if(!audit)assert(Object.values(checks).every(Boolean));
 }catch(e){await page.screenshot({path:path.join(out,'consistency-failure.png')});fs.writeFileSync(path.join(out,'consistency-partial.json'),JSON.stringify({checks,reports,errors},null,2));throw e;}finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
