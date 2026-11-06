// Actual Core + restricted extension PG + production React assets.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const {chromium} = require('playwright');
(async () => {
 const root=process.env.FREEAGENT_UI_TEST_ROOT, pkg=process.env.FREEAGENT_UI_AFFECTWEAVE_PACKAGE, out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!pkg||!out)throw new Error('Use the isolated runner and FREEAGENT_UI_AFFECTWEAVE_PACKAGE.');
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')), key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim(), id='cc.endview.affectweave';
 const headers={'X-FreeAgent-Key':key,Origin:info.origin,'Content-Type':'application/json'};
 async function api(route,body){const response=await fetch(info.origin+'/api/'+route,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body)});const result=await response.json();if(!response.ok)throw new Error(route+': '+result.error);return result;}
 await api('extensions',{path:pkg});
 const imported=(await api('extensions')).extensions.find(item=>item.id===id);assert.equal(imported.enabled,false);assert.equal(Boolean(imported.storage_ready),false);
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}), page=await browser.newPage({viewport:{width:1440,height:1100}}), errors=[],checks={import_disabled:true};page.on('pageerror',e=>errors.push(e.message));
 try {
  await page.goto(info.origin+'/?view=settings&section=extensions#key='+key);
  await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();
  const panel=page.getByRole('region',{name:'AffectWeave 人物管理'});
  await panel.getByRole('button',{name:'初始化独立 PostgreSQL 存储',exact:true}).click();await panel.getByRole('button',{name:'独立 PostgreSQL 存储已就绪',exact:true}).waitFor();checks.explicit_restricted_pg=true;
  await page.getByRole('switch',{name:'启用扩展',exact:true}).click();await page.locator('[role="switch"][aria-label="启用扩展"][aria-checked="true"]').waitFor();
  await panel.getByRole('textbox',{name:'人物标识',exact:true}).fill('maya');await panel.getByRole('textbox',{name:'名字',exact:true}).fill('Maya');await panel.getByRole('textbox',{name:'身份与性格',exact:true}).fill('Precise engineering partner');await panel.getByRole('textbox',{name:'边界',exact:true}).fill('不公开私人对话');
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'扩展',exact:true}).click();await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();assert.equal(await panel.getByRole('textbox',{name:'名字',exact:true}).inputValue(),'Maya');checks.character_draft_retained=true;
  await panel.getByRole('button',{name:'预览新建人物',exact:true}).click();await panel.getByRole('button',{name:'批准并继续',exact:true}).waitFor();assert.equal((await api('extensions/'+id+'/state')).roles.length,0);checks.preview_did_not_write=true;
  await panel.getByRole('button',{name:'批准并继续',exact:true}).click();await panel.getByRole('combobox',{name:'人物',exact:true}).selectOption('maya');checks.exact_role_confirmation=true;
  await panel.getByRole('button',{name:'预览设为默认人物与片段预算',exact:true}).click();await panel.getByRole('button',{name:'批准并继续',exact:true}).click();
  const chat=await api('chats',{title:'AffectWeave browser acceptance'});await page.goto(info.origin+'/?chat='+encodeURIComponent(chat.id));
  await page.getByRole('button',{name:'本轮能力',exact:true}).click();await page.getByRole('combobox',{name:'本轮人物',exact:true}).selectOption('maya');await page.keyboard.press('Escape');checks.turn_character_selection=true;
  const input=page.getByRole('textbox',{name:'消息',exact:true});await input.fill('character browser check');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: character browser check',{exact:true}).waitFor({timeout:15000});
  const state=await api('extensions/'+id+'/state?conversation_id='+chat.id+'&role_id=maya');assert.equal(state.state.revision,2);assert.equal(state.inputs.length,1);checks.actual_input_adopted_once=true;
  await page.goto(info.origin+'/?view=settings&section=extensions');await page.getByRole('navigation',{name:'扩展列表',exact:true}).getByRole('button').filter({hasText:'AffectWeave'}).click();await panel.getByRole('combobox',{name:'人物',exact:true}).selectOption('maya');
  await panel.getByRole('textbox',{name:'具体变更（JSON）',exact:true}).fill('{"relationship_delta":{"trust":0.05}}');await panel.getByRole('button',{name:'使用最近已采用输入',exact:true}).click();await panel.getByRole('button',{name:'预览状态提案',exact:true}).click();await panel.getByRole('button',{name:'拒绝',exact:true}).click();
  assert.equal((await api('extensions/'+id+'/state?role_id=maya')).state.relationship.trust,.5);checks.rejected_state_unchanged=true;
  const backup=path.join(root,'paired-browser');await panel.getByText('配套备份与恢复',{exact:true}).click();await panel.getByRole('textbox',{name:'新备份目录的绝对路径',exact:true}).fill(backup);await panel.getByRole('button',{name:'创建配套备份',exact:true}).click();await panel.getByText('Core 与人物的配套备份已完成。',{exact:true}).waitFor({timeout:30000});assert(fs.existsSync(path.join(backup,'pair.json')));checks.paired_backup_ui=true;
  await page.screenshot({path:path.join(out,'affectweave-management.png'),fullPage:true});checks.no_runtime_errors=errors.length===0;fs.writeFileSync(path.join(out,'affectweave-results.json'),JSON.stringify({checks,errors},null,2));console.log(JSON.stringify({checks,errors}));assert(Object.values(checks).every(Boolean));
 } finally {await browser.close();}
})().catch(error=>{console.error(error.message);process.exitCode=1});
