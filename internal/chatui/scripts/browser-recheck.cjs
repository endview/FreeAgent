const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS;
if(!root||!out)throw Error('Use the isolated test-frontend.py runner');
(async()=>{
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8'));
 const key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
 const browser=await chromium.launch({headless:true,...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{})});
 const checks={},failures={};fs.mkdirSync(out,{recursive:true});
 async function api(route,body,method){const r=await fetch(info.origin+'/api/'+route,{method:method||(body===undefined?'GET':'POST'),headers:{'X-FreeAgent-Key':key,Origin:info.origin,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});const data=await r.json();assert(r.ok,route+': '+JSON.stringify(data));return data;}
 async function test(name,fn){const page=await browser.newPage({viewport:{width:1440,height:1024}});page.setDefaultTimeout(8000);page.on('dialog',d=>d.accept());try{await fn(page);checks[name]=true;}catch(e){checks[name]=false;failures[name]=String(e);fs.writeFileSync(path.join(out,name+'.txt'),await page.locator('body').ariaSnapshot());}finally{await page.close();}}
 async function open(page,section,chat=''){await page.goto(info.origin+'/?view=settings&section='+section+(chat?'&chat='+chat:'')+'#key='+key);await page.locator('.settings-heading h1').waitFor();}
 async function approve(page){const approval=page.getByRole('region',{name:'待确认操作',exact:true});await approval.waitFor();await approval.getByRole('button',{name:'批准并继续',exact:true}).click();}
 try{
  await test('startup_draft_survives_delayed_environment',async page=>{
   const chat=await api('chats',{title:'Startup draft'});await page.goto(info.origin+'/?chat='+chat.id+'#key='+key);
   const input=page.getByRole('textbox',{name:'消息',exact:true});await input.fill('saved before reload');
   let release;const gate=new Promise(resolve=>release=resolve);
   await page.route(info.origin+'/api/environment',async route=>{await gate;await route.continue();},{times:1});
   await page.reload({waitUntil:'domcontentloaded'});await input.fill('typed while restoring');release();
   await page.getByRole('button',{name:'发送消息',exact:true}).waitFor();await page.waitForFunction(()=>!document.querySelector('button[aria-label="发送消息"]')?.disabled);
   assert.equal(await input.inputValue(),'typed while restoring');
   await page.reload();await page.waitForFunction(()=>!document.querySelector('button[aria-label="发送消息"]')?.disabled);assert.equal(await input.inputValue(),'typed while restoring');
  });
  await test('workspace_sequential_save_and_route',async page=>{
   const chat=await api('chats',{title:'Workspace original'}),original=(await api('environment')).workspace;
   const next=path.join(root,'second workspace');fs.mkdirSync(next);
   await open(page,'general',chat.id);
   const input=page.getByRole('textbox',{name:'工作区绝对路径（Core 所在机器）',exact:true});
   await input.fill(next);await page.getByRole('button',{name:'预览并保存',exact:true}).click();
   let saved=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/control/confirm');await approve(page);assert((await saved).ok());
   await page.getByRole('button',{name:'预览并保存',exact:true}).waitFor();
   await input.fill(original);const planned=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/control/plan');await page.getByRole('button',{name:'预览并保存',exact:true}).click();assert((await planned).ok(),'second workspace preview rejected');
   saved=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/control/confirm');await approve(page);assert((await saved).ok());
   await page.getByRole('button',{name:'预览并保存',exact:true}).waitFor();
   assert.equal((await api('environment')).workspace,original);
   assert.equal(new URL(page.url()).searchParams.get('chat'),null,'stale workspace chat retained in URL');
  });
  await test('attachment_conflict_has_recovery',async page=>{
   await open(page,'attachments');const field=page.getByRole('spinbutton',{name:'每批最多文件数',exact:true});await field.fill('3');
   const old=await api('attachment-policy');await api('attachment-policy',{...old,max_files:4},'PUT');
   await page.getByRole('button',{name:'保存附件策略',exact:true}).click();await page.getByRole('alert').filter({hasText:'控制目标已变化'}).waitFor();assert.equal(await field.inputValue(),'3');
   await page.getByRole('button',{name:'读取最新附件策略',exact:true}).click();assert.equal(await field.inputValue(),'4');
   await field.fill('5');await page.getByRole('button',{name:'保存附件策略',exact:true}).click();await page.getByRole('status').filter({hasText:'附件策略已保存'}).waitFor();assert.equal((await api('attachment-policy')).max_files,5);
  });
  await test('public_conflict_has_recovery',async page=>{
   await open(page,'public');const field=page.getByRole('spinbutton',{name:'失败次数',exact:true});await field.fill('9');
   const old=await api('public-settings');await api('public-settings',{...old.config,security:{...old.config.security,failure_limit:7},expected_revisions:old.expected_revisions},'PUT');
   await page.getByRole('button',{name:'保存公网设置',exact:true}).click();await page.getByRole('alert').filter({hasText:'控制目标已变化'}).waitFor();assert.equal(await field.inputValue(),'9');
   await page.getByRole('button',{name:'读取最新公网设置',exact:true}).click();assert.equal(await field.inputValue(),'7');
   await field.fill('8');await page.getByRole('button',{name:'保存公网设置',exact:true}).click();await page.getByRole('status').filter({hasText:'公网设置已保存'}).waitFor();assert.equal((await api('public-settings')).config.security.failure_limit,8);
  });
  await test('confirmation_lost_receipt_is_reconciled',async page=>{
   await open(page,'advanced');const details=page.locator('details').filter({has:page.locator('summary').filter({hasText:/^Memory 预算$/})}).first();await details.locator('summary').first().click();await details.getByRole('spinbutton',{name:'最多条目',exact:true}).fill('17');
   await details.getByRole('button',{name:'预览并保存',exact:true}).click();
   let confirms=0;await page.route(info.origin+'/api/control/confirm',async route=>{confirms++;await route.fetch();await route.abort('connectionreset');},{times:1});
   await approve(page);await page.getByRole('status').filter({hasText:'已确认并保存设置'}).waitFor();assert.equal((await api('control/config')).memory.max_entries,17);assert.equal(confirms,1);
  });
  await test('confirmation_without_receipt_stays_pending',async page=>{
   await open(page,'advanced');const details=page.locator('details').filter({has:page.locator('summary').filter({hasText:/^Memory 预算$/})}).first();await details.locator('summary').first().click();await details.getByRole('spinbutton',{name:'最多条目',exact:true}).fill('18');
   await details.getByRole('button',{name:'预览并保存',exact:true}).click();
   await page.route(info.origin+'/api/control/confirm',route=>route.abort('connectionreset'),{times:1});
   await approve(page);await details.getByRole('alert').filter({hasText:/fetch|网络/}).waitFor();assert.equal((await api('control/config')).memory.max_entries,17);
   await approve(page);await page.getByRole('status').filter({hasText:'已确认并保存设置'}).waitFor();assert.equal((await api('control/config')).memory.max_entries,18);
  });
  await test('delivery_conflict_has_recovery',async page=>{
   const {expected_revisions: _baselineRevision,...baseline}=await api('settings/delivery');
   // A disabled historical rule keeps the editor available without starting a channel.
   const rule={id:'review-inactive',extension:'com.example.review',conversation_id:(await api('chats',{title:'Inactive delivery review'})).id,enabled:false,timezone:'UTC',quiet_start:'',quiet_end:'',daily_max:1,cooldown_hours:1,allow_sticker:false,cancel_on_input:true,max_delay_seconds:60};
   async function apply(args){const op=await api('control/plan',{kind:'delivery.set_policy',arguments:args});const confirm=await api('control/confirmation?operation_id='+op.operation_id);await api('control/confirm',{operation_id:op.operation_id,code:confirm.code});}
   await apply({rules:[rule]});await open(page,'delivery');const field=page.getByRole('spinbutton',{name:'各部分最短间隔（毫秒）',exact:true});await field.fill('400');
   await apply({interval_ms:0});await page.getByRole('button',{name:'预览并保存',exact:true}).click();await page.getByRole('alert').filter({hasText:'控制目标已变化'}).waitFor();assert.equal(await field.inputValue(),'400');
   const reread=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/settings/delivery');await page.getByRole('button',{name:'读取最新投递授权',exact:true}).click();await reread;
   await page.waitForFunction(()=>[...document.querySelectorAll('label')].find(x=>x.textContent.includes('各部分最短间隔'))?.querySelector('input')?.value==='0');
   assert.equal(await field.inputValue(),'0');await field.fill('500');await page.getByRole('button',{name:'预览并保存',exact:true}).click();await approve(page);await page.getByRole('status').filter({hasText:'投递授权已保存'}).waitFor();assert.equal((await api('settings/delivery')).interval_ms,500);
   await apply({...baseline,rules:baseline.rules||[],interval_ms:baseline.interval_ms||0});
  });
  await test('memory_lost_receipt_does_not_duplicate',async page=>{
   const chat=await api('chats',{title:'Memory receipt'});await open(page,'memory',chat.id);
   await page.getByRole('textbox',{name:'Memory 标题',exact:true}).fill('unique memory');await page.getByRole('textbox',{name:'Memory 正文',exact:true}).fill('reviewed body');
   await page.route(info.origin+'/api/attach/memory',async route=>{if(route.request().postDataJSON()?.command==='save'){await route.fetch();await route.abort('connectionreset');}else await route.continue();},{times:1});
   await page.getByRole('button',{name:'确认保存 Memory',exact:true}).click();await page.getByRole('alert').filter({hasText:/fetch|网络/}).waitFor();
   await page.getByRole('button',{name:'确认保存 Memory',exact:true}).click();await page.waitForFunction(()=>document.querySelector('input[maxlength="256"]')?.value==='');
   const entries=await api('attach/memory',{command:'list',conversation_id:chat.id,scope_kind:'private',limit:64});assert.equal(entries.length,1,'lost receipt created duplicate retained memory');
  });
  await test('confirmation_load_failure_can_retry',async page=>{
   await open(page,'advanced');const details=page.locator('details').filter({has:page.locator('summary').filter({hasText:/^Memory 预算$/})}).first();await details.locator('summary').first().click();await details.getByRole('spinbutton',{name:'最多条目',exact:true}).fill('19');
   await page.route('**/api/control/confirmation?*',route=>route.abort('connectionreset'),{times:1});
   await details.getByRole('button',{name:'预览并保存',exact:true}).click();await details.getByRole('alert').waitFor();
   assert(await details.getByRole('button',{name:'批准并继续',exact:true}).isDisabled());
   await details.getByRole('button',{name:'重新读取确认状态',exact:true}).click();await approve(page);await page.getByRole('status').filter({hasText:'已确认并保存设置'}).waitFor();assert.equal((await api('control/config')).memory.max_entries,19);
  });
  await test('reload_failure_preserves_settings_draft',async page=>{
   await open(page,'advanced');let details=page.locator('details').filter({has:page.locator('summary').filter({hasText:/^Memory 预算$/})}).first();await details.locator('summary').first().click();await details.getByRole('spinbutton',{name:'最多条目',exact:true}).fill('21');
   await page.route(info.origin+'/api/control/config',route=>route.abort('connectionreset'),{times:1});await details.getByRole('button',{name:'重新读取此项',exact:true}).click();
   await details.getByRole('alert').filter({hasText:/fetch|网络/}).waitFor();assert.equal(await details.getByRole('spinbutton',{name:'最多条目',exact:true}).inputValue(),'21');
   await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'通用',exact:true}).click();await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'高级策略',exact:true}).click();
   details=page.locator('details').filter({has:page.locator('summary').filter({hasText:/^Memory 预算$/})}).first();await details.locator('summary').first().click();assert.equal(await details.getByRole('spinbutton',{name:'最多条目',exact:true}).inputValue(),'21');
   await details.getByRole('button',{name:'重新读取此项',exact:true}).click();await page.waitForFunction(()=>[...document.querySelectorAll('label')].find(x=>x.textContent.includes('最多条目'))?.querySelector('input')?.value==='19');
  });
  await test('reload_preserves_edits_made_while_waiting',async page=>{
   await open(page,'advanced');const details=page.locator('details').filter({has:page.locator('summary').filter({hasText:/^Memory 预算$/})}).first();await details.locator('summary').first().click();const field=details.getByRole('spinbutton',{name:'最多条目',exact:true});await field.fill('22');
   let release,received;const gate=new Promise(r=>release=r),seen=new Promise(r=>received=r);
   await page.route(info.origin+'/api/control/config',async route=>{const response=await route.fetch();received();await gate;await route.fulfill({response});},{times:1});
   await details.getByRole('button',{name:'重新读取此项',exact:true}).click();await seen;await field.fill('23');release();await details.getByRole('alert').filter({hasText:'草稿已修改'}).waitFor();assert.equal(await field.inputValue(),'23');assert.equal((await api('control/config')).memory.max_entries,19);
  });
 }finally{fs.writeFileSync(path.join(out,'recheck.json'),JSON.stringify({checks,failures},null,2));console.log(JSON.stringify({checks,failures}));await browser.close();}
 if(Object.values(checks).some(x=>!x))process.exitCode=1;
})().catch(e=>{console.error(e);process.exitCode=1});
