const fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');
const assert=require('node:assert/strict');
const artifacts=process.env.FREEAGENT_UI_ARTIFACTS;
if(!artifacts||!process.env.FREEAGENT_UI_TEST_ROOT)throw new Error('Use scripts/test-frontend.py with an isolated Core.');
fs.mkdirSync(artifacts,{recursive:true});
(async()=>{
 const root=path.join(process.env.FREEAGENT_UI_TEST_ROOT,'data'),info=JSON.parse(fs.readFileSync(root+'/engine.json','utf8')),key=fs.readFileSync(root+'/chat.secret','utf8').trim();
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}),page=await browser.newPage({viewport:{width:1440,height:1024}});
 const errors=[],failures=[],checks={};page.on('pageerror',e=>errors.push(e.message));page.on('response',r=>{if(r.url().includes('/api/')&&r.status()>=400)failures.push({path:new URL(r.url()).pathname,status:r.status()});});
 const confirmations=[];page.on('request',request=>{if(new URL(request.url()).pathname==='/api/control/confirm')confirmations.push(request.postDataJSON().operation_id);});
 async function api(route,body){const response=await fetch(info.origin+'/api/'+route,{method:body?'POST':'GET',headers:{'X-FreeAgent-Key':key,'Origin':info.origin,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const value=await response.json();assert(response.ok,'isolated fixture API '+route+': '+(value.error||response.status));return value;}
 const policy=({expected_revisions,...value})=>({generation_enabled:{},sticker_limit:1,interval_ms:0,enabled:false,rules:[],...value});
 async function choose(label,option){await page.getByRole('combobox',{name:label,exact:true}).click();await page.getByRole('option',{name:option,exact:true}).click();}
 async function previewDelivery(){const responsePromise=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/control/plan'&&response.request().postDataJSON()?.kind==='delivery.set_policy');await page.getByRole('button',{name:'预览并保存',exact:true}).click();const response=await responsePromise;assert(response.ok(),'delivery preview');const operation=await response.json();await page.getByRole('region',{name:'待确认操作',exact:true}).waitFor();await page.waitForFunction(()=>document.querySelector('.approval-footer button:last-child')?.disabled===false);return {operation,arguments:response.request().postDataJSON().arguments};}
 page.on('dialog',dialog=>dialog.accept());
 try{
  await page.goto(info.origin+'/?view=settings&section=general#key='+key);
  const names=['通用','空间与会话','模型与 Key','权限与执行','文件范围','命令执行','后台命令','上下文','内置工具','MCP','Skill','Memory 与固定内容','扩展','渠道','投递授权','公网安全','数据库与备份','附件','诊断','高级策略'];
  for(const name of names){await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name,exact:true}).click();await page.locator('.settings-heading h1').filter({hasText:name}).waitFor();await page.waitForTimeout(250);}
  checks.all_pages_open=errors.length===0&&failures.length===0;
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'上下文',exact:true}).click();
  await page.getByRole('spinbutton',{name:'默认输出 token 上限',exact:true}).fill('4097');await page.getByRole('button',{name:'保存设置',exact:true}).click();
  await page.getByRole('status').filter({hasText:'设置已保存'}).waitFor();checks.context_save=true;
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'内置工具',exact:true}).click();
  await page.getByRole('checkbox',{name:'网络工具',exact:true}).check();await page.getByRole('textbox',{name:'允许的网络来源（每行一个）',exact:true}).fill('https://example.com');await page.getByRole('button',{name:'保存设置',exact:true}).click();
  await page.getByRole('status').filter({hasText:'设置已保存'}).waitFor();checks.enable_network=true;
  await page.getByRole('checkbox',{name:'网络工具',exact:true}).uncheck();await page.getByRole('button',{name:'保存设置',exact:true}).click();await page.getByRole('status').filter({hasText:'设置已保存'}).waitFor();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'公网安全',exact:true}).click();
  await page.getByRole('checkbox',{name:'登录 Cookie 回传检测',exact:true}).waitFor();checks.cookie_policy_visible=true;
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'高级策略',exact:true}).click();
  await page.getByText('自动模型路由',{exact:true}).click();await page.getByRole('button',{name:'预览并保存',exact:true}).first().click();await page.getByRole('region',{name:'待确认操作',exact:true}).waitFor();await page.getByRole('button',{name:'拒绝',exact:true}).click();await page.getByRole('region',{name:'待确认操作',exact:true}).waitFor({state:'hidden'});checks.control_preview_reject=true;
  let initialDelivery=policy(await api('settings/delivery'));assert.equal(initialDelivery.enabled,false);assert.deepEqual(initialDelivery.rules,[]);
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'投递授权',exact:true}).click();
  await page.getByText('当前没有已启用的投递渠道。请先在渠道中配置连接；已有交付记录可在对应会话查看。',{exact:true}).waitFor();checks.no_channel_has_no_delivery_editor=true;
  const history=await api('control/plan',{kind:'delivery.set_policy',arguments:policy({rules:[{id:'historical-rule',extension:'com.example.history',conversation_id:(await api('chats',{title:'Historical inactive grant'})).id,enabled:false,timezone:'UTC',quiet_start:'',quiet_end:'',daily_max:1,cooldown_hours:1,allow_sticker:false,cancel_on_input:true,max_delay_seconds:60}]})});
  const historyConfirmation=await api('control/confirmation?operation_id='+history.operation_id);
  await api('control/confirm',{operation_id:history.operation_id,code:historyConfirmation.code});
  initialDelivery=policy(await api('settings/delivery'));
  await page.reload();
  const activeFollowups=page.getByRole('checkbox',{name:'允许已授权扩展的一次性主动跟进',exact:true});await activeFollowups.waitFor();assert.equal(await activeFollowups.isChecked(),false);checks.delivery_default_off=true;
  await page.getByRole('button',{name:'删除此规则',exact:true}).click();
  const deliveryChat=await api('chats',{title:'Delivery browser confirmation fixture'});
  assert.equal(await page.getByRole('combobox',{name:'默认回复形式',exact:true}).count(),0);checks.core_has_no_expression_policy=true;
  await page.getByRole('spinbutton',{name:'本轮贴纸选择上限',exact:true}).fill('2');await page.getByRole('spinbutton',{name:'各部分最短间隔（毫秒）',exact:true}).fill('250');await activeFollowups.check();
  await page.getByRole('button',{name:'新增跟进规则',exact:true}).click();
  await page.getByRole('textbox',{name:'规则标识',exact:true}).fill('browser-followup');await page.getByRole('textbox',{name:'扩展标识',exact:true}).fill('com.example.browser');await page.getByRole('textbox',{name:/^目标对话/}).fill(deliveryChat.id);await page.getByRole('textbox',{name:'时区',exact:true}).fill('Asia/Shanghai');
  await page.getByLabel('静默开始',{exact:true}).fill('22:30');await page.getByLabel('静默结束',{exact:true}).fill('07:15');
  await page.getByRole('spinbutton',{name:'目标冷却（小时）',exact:true}).fill('8');await page.getByRole('spinbutton',{name:'最长预约间隔（秒）',exact:true}).fill('7200');
  await page.getByRole('checkbox',{name:'启用规则',exact:true}).check();await page.getByRole('checkbox',{name:'允许一张已授权贴纸',exact:true}).check();await page.getByRole('checkbox',{name:'收到新输入就取消跟进',exact:true}).uncheck();
  const expectedPolicy={generation_enabled:{},sticker_limit:2,interval_ms:250,enabled:true,rules:[{id:'browser-followup',extension:'com.example.browser',conversation_id:deliveryChat.id,enabled:true,timezone:'Asia/Shanghai',quiet_start:'22:30',quiet_end:'07:15',daily_max:1,cooldown_hours:8,allow_sticker:true,cancel_on_input:false,max_delay_seconds:7200}]};
  const firstPreview=await previewDelivery();assert.deepEqual(firstPreview.arguments,expectedPolicy);assert.deepEqual(policy(JSON.parse(firstPreview.operation.proposed_values['config.delivery'])),expectedPolicy);
  const approval=page.getByRole('region',{name:'待确认操作',exact:true});assert((await approval.innerText()).includes('browser-followup')&&(await approval.innerText()).includes('Asia/Shanghai'));assert.deepEqual(policy(await api('settings/delivery')),initialDelivery);checks.delivery_preview_exact_and_unapplied=true;
  const confirmationsBefore=confirmations.length;
  await page.getByRole('spinbutton',{name:'目标每日上限',exact:true}).fill('2');await approval.waitFor({state:'hidden'});await page.getByRole('button',{name:'预览并保存',exact:true}).waitFor();
  assert.equal(confirmations.length,confirmationsBefore);assert.deepEqual(policy(await api('settings/delivery')),initialDelivery);checks.delivery_edit_invalidates_old_approval=true;
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'上下文',exact:true}).click();await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'投递授权',exact:true}).click();
  assert.equal(await page.getByRole('spinbutton',{name:'目标每日上限',exact:true}).inputValue(),'2');assert.equal(await activeFollowups.isChecked(),true);checks.delivery_draft_survives_navigation=true;
  expectedPolicy.rules[0].daily_max=2;const nextPreview=await previewDelivery();assert.notEqual(nextPreview.operation.operation_id,firstPreview.operation.operation_id);assert.deepEqual(nextPreview.arguments,expectedPolicy);assert.deepEqual(policy(JSON.parse(nextPreview.operation.proposed_values['config.delivery'])),expectedPolicy);
  await approval.getByRole('button',{name:'批准并继续',exact:true}).click();await page.getByRole('status').filter({hasText:'投递授权已保存'}).waitFor();
  assert.deepEqual(confirmations.slice(confirmationsBefore),[nextPreview.operation.operation_id]);assert.deepEqual(policy(await api('settings/delivery')),expectedPolicy);checks.delivery_confirmation_persists_exact_policy=true;
  await page.reload();await activeFollowups.waitFor();assert.equal(await activeFollowups.isChecked(),true);assert.equal(await page.getByRole('spinbutton',{name:'目标每日上限',exact:true}).inputValue(),'2');assert.equal(await page.getByRole('textbox',{name:'时区',exact:true}).inputValue(),'Asia/Shanghai');assert.equal(await page.getByRole('checkbox',{name:'收到新输入就取消跟进',exact:true}).isChecked(),false);assert.deepEqual(policy(await api('settings/delivery')),expectedPolicy);checks.delivery_policy_survives_reload=true;
  await page.setViewportSize({width:390,height:844});const settingsBack=page.getByRole('button',{name:'返回设置列表',exact:true});if(await settingsBack.isVisible())await settingsBack.click();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();await page.locator('.settings-heading h1').getByText('模型与 Key',{exact:true}).waitFor();
  await settingsBack.click();await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();await page.locator('.settings-heading h1').getByText('模型与 Key',{exact:true}).waitFor();checks.mobile_secondary_navigation=true;
  checks.no_runtime_errors=errors.length===0;checks.no_api_failures=failures.length===0;
  await page.screenshot({path:path.join(artifacts,'freeagent-v81-settings.png')});console.log(JSON.stringify({checks,errors,failures}));fs.writeFileSync(path.join(artifacts,'v81-settings-results.json'),JSON.stringify({checks,errors,failures},null,2));
  if(!Object.values(checks).every(Boolean))throw new Error('settings checks failed');
 }catch(error){console.log(JSON.stringify({checks,errors,failures}));await page.screenshot({path:path.join(artifacts,'freeagent-v81-settings-failure.png')});throw error;}
 finally{await browser.close();}
})().catch(e=>{console.error(e.message);process.exitCode=1;});
