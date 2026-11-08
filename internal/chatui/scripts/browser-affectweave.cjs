// Actual Core + restricted extension PG + production React assets.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const {chromium} = require('playwright');
(async () => {
 const root=process.env.FREEAGENT_UI_TEST_ROOT, pkg=process.env.FREEAGENT_UI_AFFECTWEAVE_PACKAGE, out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!pkg||!out)throw new Error('Use the isolated runner and FREEAGENT_UI_AFFECTWEAVE_PACKAGE.');
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')), key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim(), id='cc.endview.affectweave';
 const headers={'X-FreeAgent-Key':key,Origin:info.origin,'Content-Type':'application/json'};
 async function api(route,body){const response=await fetch(info.origin+'/api/'+route,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body)});const result=await response.json();if(!response.ok)throw new Error(route+': '+result.error);return result;}
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}), page=await browser.newPage({viewport:{width:1440,height:1100}}), errors=[],checks={};page.on('pageerror',e=>errors.push(e.message));
 let chatID='';
 try {
  const plain=await api('chats',{title:'Plain Core expression boundary'});
  await page.goto(info.origin+'/?chat='+plain.id+'#key='+key);await page.getByRole('textbox',{name:'消息',exact:true}).waitFor();
  assert.equal(await page.getByRole('combobox',{name:/回复方式|本轮回复方式/}).count(),0);checks.no_core_expression_selector=true;
  await api('extensions',{path:pkg});
  const imported=(await api('extensions')).extensions.find(item=>item.id===id);assert.equal(imported.enabled,false);assert.equal(Boolean(imported.storage_ready),false);checks.import_disabled=true;
  await page.reload();await page.getByRole('textbox',{name:'消息',exact:true}).waitFor();assert.equal(await page.getByRole('combobox',{name:'AffectWeave：回复方式',exact:true}).count(),0);checks.disabled_extension_no_selector=true;
  await page.goto(info.origin+'/?view=settings&section=extensions#key='+key);
  await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();
  const panel=page.getByRole('region',{name:'AffectWeave 人物管理'});
  await panel.getByRole('button',{name:'初始化独立 PostgreSQL 存储',exact:true}).click();await panel.getByRole('button',{name:'独立 PostgreSQL 存储已就绪',exact:true}).waitFor();checks.explicit_restricted_pg=true;
  await page.getByRole('switch',{name:'启用扩展',exact:true}).click();await page.locator('[role="switch"][aria-label="启用扩展"][aria-checked="true"]').waitFor();
  await page.getByRole('combobox',{name:'AffectWeave：默认回复方式',exact:true}).selectOption('paragraphs');
  const saveSettings=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/extensions/'+id+'/update');await page.getByRole('button',{name:'保存扩展配置',exact:true}).click();assert((await saveSettings).ok());assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.reply_mode,'paragraphs');checks.extension_owns_default_expression=true;
  await panel.getByRole('textbox',{name:'人物标识',exact:true}).fill('maya');await panel.getByRole('textbox',{name:'名字',exact:true}).fill('Maya');await panel.getByRole('textbox',{name:'身份与性格',exact:true}).fill('Precise engineering partner');await panel.getByRole('textbox',{name:'边界',exact:true}).fill('不公开私人对话');
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'扩展',exact:true}).click();await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();assert.equal(await panel.getByRole('textbox',{name:'名字',exact:true}).inputValue(),'Maya');checks.character_draft_retained=true;
  await panel.getByRole('button',{name:'预览新建人物',exact:true}).click();await panel.getByRole('button',{name:'批准并继续',exact:true}).waitFor();assert.equal((await api('extensions/'+id+'/state')).roles.length,0);checks.preview_did_not_write=true;
  await panel.getByRole('button',{name:'批准并继续',exact:true}).click();await panel.getByRole('combobox',{name:'人物',exact:true}).selectOption('maya');checks.exact_role_confirmation=true;
  const analysisToggle=panel.getByRole('checkbox',{name:/自动输入分析/});
  await analysisToggle.check();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'扩展',exact:true}).click();
  await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();
  assert.equal(await analysisToggle.isChecked(),true);checks.analysis_draft_retained=true;
  await panel.getByRole('button',{name:'预览分析开关修改',exact:true}).click();
  await panel.getByRole('button',{name:'批准并继续',exact:true}).click();
  await panel.getByRole('button',{name:'批准并继续',exact:true}).waitFor({state:'detached'});
  assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.analysis_enabled,true);checks.explicit_analysis_confirmation=true;

  await panel.getByRole('button',{name:'预览设为默认人物与片段预算',exact:true}).click();await panel.getByRole('button',{name:'批准并继续',exact:true}).click();
  await panel.getByRole('button',{name:'批准并继续',exact:true}).waitFor({state:'detached'});
  assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.role_id,'maya');checks.extension_owns_character_setting=true;
  assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.reply_mode,'paragraphs');checks.character_selection_keeps_expression_settings=true;
  const chat=await api('chats',{title:'AffectWeave browser acceptance'});chatID=chat.id;await page.goto(info.origin+'/?chat='+encodeURIComponent(chat.id));
  await page.getByRole('button',{name:'本轮能力',exact:true}).click();await page.getByRole('combobox',{name:'本轮人物',exact:true}).selectOption('maya');await page.keyboard.press('Escape');checks.turn_character_selection=true;
  const replyChoice=page.getByRole('combobox',{name:'AffectWeave：回复方式',exact:true});await replyChoice.waitFor();
  await page.getByRole('button',{name:'本轮能力',exact:true}).click();
  let savedSelection=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/chats/'+chat.id+'/capabilities');await page.getByRole('checkbox',{name:id,exact:true}).click();assert((await savedSelection).ok());await replyChoice.waitFor({state:'detached'});checks.unselected_extension_no_selector=true;
  savedSelection=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/chats/'+chat.id+'/capabilities');await page.getByRole('checkbox',{name:id,exact:true}).click();assert((await savedSelection).ok());await replyChoice.waitFor();await page.keyboard.press('Escape');await replyChoice.selectOption('single');
  const submitted=page.waitForRequest(request=>new URL(request.url()).pathname==='/api/chats/'+chat.id+'/submit');
  const input=page.getByRole('textbox',{name:'消息',exact:true});await input.fill('character browser check');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: character browser check',{exact:true}).waitFor({timeout:15000});
  const submittedBody=(await submitted).postDataJSON();assert.equal(submittedBody.capabilities.extension_settings[id].reply_mode,'single');assert.equal(Object.hasOwn(submittedBody,'reply_mode'),false);await page.waitForFunction(()=>document.querySelector('select[aria-label="AffectWeave：回复方式"]')?.value==='');assert.equal((await api('extensions')).extensions.find(item=>item.id===id).settings.reply_mode,'paragraphs');checks.turn_setting_is_generic_and_not_persistent=true;
  await page.setViewportSize({width:390,height:844});assert(await replyChoice.isVisible());assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1));checks.narrow_extension_control=true;await page.setViewportSize({width:1440,height:1100});
  const state=await api('extensions/'+id+'/state?conversation_id='+chat.id+'&resource=maya');assert.equal(state.state.revision,2);assert.equal(state.inputs.length,1);checks.actual_input_adopted_once=true;
  await page.goto(info.origin+'/?view=settings&section=extensions');await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();await panel.getByRole('combobox',{name:'人物',exact:true}).selectOption('maya');
  await panel.getByRole('textbox',{name:'具体变更（JSON）',exact:true}).fill('{"relationship_delta":{"trust":0.05}}');await panel.getByRole('button',{name:'使用最近已采用输入',exact:true}).click();await panel.getByRole('button',{name:'预览状态提案',exact:true}).click();await panel.getByRole('button',{name:'拒绝',exact:true}).click();
  assert.equal((await api('extensions/'+id+'/state?resource=maya')).state.relationship.trust,.5);checks.rejected_state_unchanged=true;
  const backup=path.join(root,'paired-browser');await panel.getByText('配套备份与恢复',{exact:true}).click();await panel.getByRole('textbox',{name:'新备份目录的绝对路径',exact:true}).fill(backup);await panel.getByRole('button',{name:'创建配套备份',exact:true}).click();await panel.getByText('Core 与人物的配套备份已完成。',{exact:true}).waitFor({timeout:30000});assert(fs.existsSync(path.join(backup,'pair.json')));checks.paired_backup_ui=true;
  await page.screenshot({path:path.join(out,'affectweave-management.png'),fullPage:true});
  await page.getByRole('switch',{name:'启用扩展',exact:true}).click();await page.locator('[role="switch"][aria-label="启用扩展"][aria-checked="false"]').waitFor();await page.goto(info.origin+'/?chat='+chat.id);await input.waitFor();assert.equal(await replyChoice.count(),0);await input.fill('Core still works after disabling AffectWeave');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: Core still works after disabling AffectWeave',{exact:true}).waitFor({timeout:15000});checks.disable_revokes_ui_and_core_keeps_working=true;
  checks.no_runtime_errors=errors.length===0;fs.writeFileSync(path.join(out,'affectweave-results.json'),JSON.stringify({checks,errors},null,2));console.log(JSON.stringify({checks,errors}));assert(Object.values(checks).every(Boolean));
 } catch(error) {
  // This isolated runner contains synthetic data only; retain the visible
  // failure instead of reducing an unsuccessful Run to a locator timeout.
  await page.screenshot({path:path.join(out,'affectweave-failure.png'),fullPage:true});
  fs.writeFileSync(path.join(out,'affectweave-failure.txt'),await page.locator('body').innerText());
  if(chatID)fs.writeFileSync(path.join(out,'affectweave-failure-state.json'),JSON.stringify(await api('chats/'+chatID),null,2));
  console.error((await page.locator('body').innerText()).slice(-7000));
  throw error;
 } finally {await browser.close();}
})().catch(error=>{console.error(error.message);process.exitCode=1});
