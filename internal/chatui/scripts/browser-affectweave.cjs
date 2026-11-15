// Actual Core + restricted extension PG + production React assets.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const {chromium} = require('playwright');
(async () => {
 const root=process.env.FREEAGENT_UI_TEST_ROOT, pkg=process.env.FREEAGENT_UI_AFFECTWEAVE_PACKAGE, out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!pkg||!out)throw new Error('Use the isolated runner and FREEAGENT_UI_AFFECTWEAVE_PACKAGE.');
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')), key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim(), id='cc.endview.affectweave';
 const headers={'X-FreeAgent-Key':key,Origin:info.origin,'Content-Type':'application/json'};
 async function api(route,body){const response=await fetch(info.origin+'/api/'+route,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body)});const result=await response.json();if(!response.ok)throw new Error(route+': '+result.error);return result;}
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}), page=await browser.newPage({viewport:{width:1440,height:1100}}), errors=[],checks={};page.on('pageerror',e=>errors.push(e.message));const stateReads=[];page.on('response',async r=>{if(!r.url().includes('/api/'))return;const url=new URL(r.url()),route=url.pathname+url.search;const stateRead=r.request().method()==='GET'&&url.pathname==='/api/extensions/'+id+'/state';const position=stateReads.length;if(stateRead)stateReads.push({route,status:r.status()});if(r.status()>=400){const failure={route,status:r.status(),stateRead,position};errors.push(failure);failure.body=await r.text().catch(()=>'(response unavailable after navigation)');}});
 let chatID='';
 try {
  const plain=await api('chats',{title:'Plain Core expression boundary'});
  await page.goto(info.origin+'/?chat='+plain.id+'#key='+key);await page.getByRole('textbox',{name:'消息',exact:true}).waitFor();
  assert.equal(await page.getByRole('combobox',{name:/回复方式|本轮回复方式/}).count(),0);checks.no_core_expression_selector=true;
  await api('extensions',{path:pkg});
  const imported=(await api('extensions')).extensions.find(item=>item.id===id);assert.equal(imported.enabled,false);assert.equal(Boolean(imported.storage_ready),false);checks.import_disabled=true;
  await page.reload();await page.getByRole('textbox',{name:'消息',exact:true}).waitFor();assert.equal(await page.getByRole('combobox',{name:'AffectWeave：回复方式',exact:true}).count(),0);checks.disabled_extension_no_selector=true;
  await page.goto(info.origin+'/?view=settings&section=extensions&chat='+plain.id+'#key='+key);
  await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();
  const panel=page.getByRole('region',{name:'AffectWeave 人物管理'});
  await panel.getByRole('button',{name:'初始化独立 PostgreSQL 存储',exact:true}).click();await panel.getByRole('button',{name:'独立 PostgreSQL 存储已就绪',exact:true}).waitFor();checks.explicit_restricted_pg=true;
  await page.getByRole('switch',{name:'启用扩展',exact:true}).click();await page.locator('[role="switch"][aria-label="启用扩展"][aria-checked="true"]').waitFor();
  await page.getByRole('combobox',{name:'AffectWeave：默认回复方式',exact:true}).selectOption('paragraphs');
  const saveSettings=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/extensions/'+id+'/update');await page.getByRole('button',{name:'保存扩展默认设置',exact:true}).click();assert((await saveSettings).ok());assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.reply_mode,'paragraphs');checks.extension_owns_default_expression=true;
  await panel.getByRole('textbox',{name:'人物标识',exact:true}).fill('maya');await panel.getByRole('textbox',{name:'名字',exact:true}).fill('Maya');await panel.getByRole('textbox',{name:'身份与性格',exact:true}).fill('Precise engineering partner');await panel.getByRole('textbox',{name:'边界',exact:true}).fill('不公开私人对话');
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'扩展',exact:true}).click();await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();assert.equal(await panel.getByRole('textbox',{name:'名字',exact:true}).inputValue(),'Maya');checks.character_draft_retained=true;
  await panel.getByRole('button',{name:'预览新建人物',exact:true}).click();await panel.getByRole('button',{name:'批准并继续',exact:true}).waitFor();assert.equal((await api('extensions/'+id+'/state')).roles.length,0);checks.preview_did_not_write=true;
  await panel.getByRole('button',{name:'批准并继续',exact:true}).click();await panel.getByRole('combobox',{name:'人物',exact:true}).selectOption('maya');checks.exact_role_confirmation=true;assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings?.role_id,undefined);checks.created_role_is_not_implicitly_default=true;
  await panel.getByRole('button',{name:'预览用于当前对话',exact:true}).click();await panel.getByRole('button',{name:'批准并继续',exact:true}).click();await panel.getByRole('button',{name:'批准并继续',exact:true}).waitFor({state:'detached'});assert.equal((await api('chats/'+plain.id)).view.capabilities.extension_variants[id],'maya');assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings?.role_id,undefined);checks.conversation_character_is_separate_from_global_default=true;
  await panel.getByText('默认人物与自动分析',{exact:false}).first().click();
  const analysisToggle=panel.getByRole('checkbox',{name:/自动输入分析/});
  await analysisToggle.check();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'扩展',exact:true}).click();
  await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();
  await panel.getByText('默认人物与自动分析',{exact:false}).first().click();assert.equal(await analysisToggle.isChecked(),true);checks.analysis_draft_retained=true;
  await panel.getByRole('button',{name:'预览分析开关修改',exact:true}).click();
  await panel.getByRole('button',{name:'批准并继续',exact:true}).click();
  await panel.getByRole('button',{name:'批准并继续',exact:true}).waitFor({state:'detached'});
  assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.analysis_enabled,true);checks.explicit_analysis_confirmation=true;

  await panel.getByRole('button',{name:'预览设为默认人物与片段预算',exact:true}).click();await panel.getByRole('button',{name:'批准并继续',exact:true}).click();
  await panel.getByRole('button',{name:'批准并继续',exact:true}).waitFor({state:'detached'});
  assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.role_id,'maya');checks.extension_owns_character_setting=true;
  assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.reply_mode,'paragraphs');checks.character_selection_keeps_expression_settings=true;
  await page.getByText('扩展运行与高级配置',{exact:true}).click();await page.waitForFunction(()=>{const e=[...document.querySelectorAll('label')].find(x=>x.textContent.includes('扩展设置（JSON）'))?.querySelector('textarea');try{return JSON.parse(e?.value||'{}').role_id==='maya';}catch{return false;}},{},{timeout:10000});const rawSaved=JSON.parse(await page.getByRole('textbox',{name:'扩展设置（JSON）',exact:true}).inputValue());assert.equal(rawSaved.role_id,'maya');assert.equal(rawSaved.analysis_enabled,true);checks.clean_advanced_form_follows_confirmed_settings=true;
  const chat=await api('chats',{title:'AffectWeave browser acceptance'});chatID=chat.id;await page.goto(info.origin+'/?chat='+encodeURIComponent(chat.id));
  await page.getByRole('button',{name:'本轮能力',exact:true}).click();await page.getByRole('combobox',{name:'本轮人物',exact:true}).selectOption('maya');await page.keyboard.press('Escape');checks.turn_character_selection=true;
  const replyChoice=page.getByRole('combobox',{name:'AffectWeave：回复方式',exact:true});assert.equal(await page.locator('.composer-tools select[aria-label="AffectWeave：回复方式"]').count(),0);checks.expression_outside_composer=true;
  await page.getByRole('button',{name:'本轮能力',exact:true}).click();await replyChoice.waitFor();
  assert.match(await replyChoice.locator('option[value=""]').innerText(),/继承：自然分段/);checks.inherited_expression_is_concrete=true;
  let savedSelection=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/chats/'+chat.id+'/capabilities');await page.getByRole('checkbox',{name:'AffectWeave',exact:true}).click();assert((await savedSelection).ok());await replyChoice.waitFor({state:'detached'});checks.unselected_extension_no_selector=true;
  savedSelection=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/chats/'+chat.id+'/capabilities');await page.getByRole('checkbox',{name:'AffectWeave',exact:true}).click();assert((await savedSelection).ok());await replyChoice.waitFor();await replyChoice.selectOption('single');await page.keyboard.press('Escape');
  const submitted=page.waitForRequest(request=>new URL(request.url()).pathname==='/api/chats/'+chat.id+'/submit');
  const input=page.getByRole('textbox',{name:'消息',exact:true});await input.fill('character browser check');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: character browser check',{exact:true}).waitFor({timeout:15000});
  const submittedBody=(await submitted).postDataJSON();assert.equal(submittedBody.capabilities.extension_settings[id].reply_mode,'single');assert.equal(Object.hasOwn(submittedBody,'reply_mode'),false);await page.getByRole('button',{name:'本轮能力',exact:true}).click();await replyChoice.waitFor();assert.equal(await replyChoice.inputValue(),'');await page.keyboard.press('Escape');assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.reply_mode,'paragraphs');checks.turn_setting_is_generic_and_not_persistent=true;await page.screenshot({path:path.join(out,'affectweave-composer-clean.png')});
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'本轮能力',exact:true}).click();await replyChoice.scrollIntoViewIfNeeded();assert(await replyChoice.evaluate(e=>{const r=e.getBoundingClientRect();return r.top>=0&&r.bottom<=innerHeight;}));assert(await page.locator('.capability-popover').evaluate(e=>{const r=e.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth&&r.bottom<=innerHeight&&e.scrollWidth<=e.clientWidth+1;}));await page.screenshot({path:path.join(out,'affectweave-turn-options-mobile.png')});await page.keyboard.press('Escape');assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1));checks.narrow_extension_control=true;await page.setViewportSize({width:1440,height:1100});
  const state=await api('extensions/'+id+'/state?conversation_id='+chat.id+'&resource=maya');assert.equal(state.state.revision,2);assert.equal(state.inputs.length,1);checks.actual_input_adopted_once=true;const actual=(await api('chats/'+chat.id)).view.turns.at(-1).activity.find(a=>a.kind==='model');assert(actual.extensions.some(x=>x.name===id&&x.bytes>0));checks.actual_context_usage_visible=true;
  await page.goto(info.origin+'/?view=settings&section=extensions');await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();await panel.getByRole('combobox',{name:'人物',exact:true}).selectOption('maya');
  await panel.getByText('当前状态与经历',{exact:false}).first().click();await panel.getByRole('textbox',{name:'具体变更（JSON）',exact:true}).fill('{"relationship_delta":{"trust":0.05}}');await panel.getByRole('button',{name:'使用最近已采用输入',exact:true}).click();await panel.getByRole('button',{name:'预览状态提案',exact:true}).click();await panel.getByRole('button',{name:'拒绝',exact:true}).click();
  assert.equal((await api('extensions/'+id+'/state?resource=maya')).state.relationship.trust,.5);checks.rejected_state_unchanged=true;
  const backup=path.join(root,'paired-browser');await panel.getByText('配套备份与恢复',{exact:true}).click();await panel.getByRole('textbox',{name:'新备份目录的绝对路径',exact:true}).fill(backup);await panel.getByRole('button',{name:'创建配套备份',exact:true}).click();await panel.getByText('Core 与人物的配套备份已完成。',{exact:true}).waitFor({timeout:30000});assert(fs.existsSync(path.join(backup,'pair.json')));checks.paired_backup_ui=true;
  await page.screenshot({path:path.join(out,'affectweave-management.png'),fullPage:true});
  for (const [width,height] of [[1440,900],[768,1024],[390,844],[844,390]]) {
    await page.setViewportSize({width,height});
    await page.locator('.settings-detail').evaluate(e=>e.scrollTo({top:0}));
    assert(await page.locator('.settings-detail').evaluate(e=>e.scrollWidth<=e.clientWidth+1),'extension detail overflow');
    assert(await panel.isVisible(),'extension remains visible after resize');
    const toggle=page.getByRole('switch',{name:'启用扩展',exact:true});
    const geometry=await toggle.evaluate(e=>{const r=e.getBoundingClientRect(),track=getComputedStyle(e,'::before');return {width:r.width,height:r.height,trackWidth:parseFloat(track.width),trackHeight:parseFloat(track.height)};});
    assert(geometry.width>=44&&geometry.height>=44&&geometry.trackWidth>geometry.trackHeight*1.5,'switch track and hit area must stay distinct');
    await page.screenshot({path:path.join(out,`affectweave-layout-${width}x${height}.png`)});
  }
  checks.extension_detail_resizes=true;
  await page.setViewportSize({width:1440,height:1100});
  await page.getByRole('switch',{name:'启用扩展',exact:true}).click();await page.locator('[role="switch"][aria-label="启用扩展"][aria-checked="false"]').waitFor();await page.goto(info.origin+'/?chat='+chat.id);await input.waitFor();assert.equal(await replyChoice.count(),0);await input.fill('Core still works after disabling AffectWeave');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: Core still works after disabling AffectWeave',{exact:true}).waitFor({timeout:15000});checks.disable_revokes_ui_and_core_keeps_working=true;
  const recovered=errors.filter(e=>e.stateRead&&e.status===409&&stateReads.slice(e.position+1).some(read=>read.route===e.route&&read.status===200));
  // A confirmed settings change restarts the optional process. Only a read
  // that subsequently succeeds for the identical scope counts as recovered.
  const unexpected=errors.filter(e=>!recovered.includes(e));checks.no_runtime_errors=unexpected.length===0;fs.writeFileSync(path.join(out,'affectweave-results.json'),JSON.stringify({checks,errors:unexpected,recovered_state_reads:recovered.length},null,2));console.log(JSON.stringify({checks,errors:unexpected,recovered_state_reads:recovered.length}));assert(Object.values(checks).every(Boolean));
 } catch(error) {
  // This isolated runner contains synthetic data only; retain the visible
  // failure instead of reducing an unsuccessful Run to a locator timeout.
  await page.screenshot({path:path.join(out,'affectweave-failure.png'),fullPage:true});
  fs.writeFileSync(path.join(out,'affectweave-failure.txt'),await page.locator('body').innerText());
  if(chatID)fs.writeFileSync(path.join(out,'affectweave-failure-state.json'),JSON.stringify(await api('chats/'+chatID),null,2));
  console.error(JSON.stringify({checks,errors}));console.error((await page.locator('body').innerText()).slice(-7000));
  throw error;
 } finally {await browser.close();}
})().catch(error=>{console.error(error.stack);process.exitCode=1});
