const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const {chromium}=require('playwright');
const assert=require('node:assert/strict');
const artifacts=process.env.FREEAGENT_UI_ARTIFACTS;
if(!artifacts||!process.env.FREEAGENT_UI_TEST_ROOT)throw new Error('Use scripts/test-frontend.py with an isolated Core.');
fs.mkdirSync(artifacts,{recursive:true});
(async()=>{
 const info=JSON.parse(fs.readFileSync(path.join(process.env.FREEAGENT_UI_TEST_ROOT,'data/engine.json'),'utf8'));
 const capability=fs.readFileSync(path.join(process.env.FREEAGENT_UI_TEST_ROOT,'data/chat.secret'),'utf8').trim();
 const modelRequests=[];
 const modelServer=http.createServer((req,res)=>{
  res.setHeader('Content-Type','application/json');
  if(req.method!=='POST'){res.end(JSON.stringify({data:[{id:'fixture-short'},{id:'fixture-long'}]}));return;}
  let raw='';req.on('data',chunk=>{raw+=chunk;});req.on('end',()=>{
   const body=JSON.parse(raw);modelRequests.push(body);
   const prompt=body.messages?.filter(message=>message.role==='user').at(-1)?.content||'fixture';
   res.end(JSON.stringify({id:'fixture-'+modelRequests.length,choices:[{message:{role:'assistant',content:'Fixture reply: '+prompt},finish_reason:'stop'}],usage:{prompt_tokens:16,completion_tokens:8,total_tokens:24}}));
  });
 });
 await new Promise(resolve=>modelServer.listen(0,'127.0.0.1',resolve));
 const origin='http://127.0.0.1:'+modelServer.address().port+'/v1';
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:1024}});const errors=[],checks={};
 async function environment(){const response=await fetch(info.origin+'/api/environment',{headers:{'X-FreeAgent-Key':capability,'Origin':info.origin}});assert(response.ok,'read isolated environment');return response.json();}
 async function api(route,body){const response=await fetch(info.origin+'/api/'+route,{method:body===undefined?'GET':'POST',headers:{'X-FreeAgent-Key':capability,'Origin':info.origin,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});const result=await response.json();assert(response.ok,route);return result;}
 async function choose(scope,label,option){await scope.getByRole('combobox',{name:label,exact:true}).click();await page.getByRole('option',{name:option,exact:true}).click();}
 async function modelOptions(name){const options=page.locator('.settings-model-list li').filter({hasText:name}).locator('.model-options');if(await options.getAttribute('open')===null)await options.locator('summary').click();return options;}
 page.on('pageerror',error=>errors.push(error.message));
 page.on('dialog',async dialog=>{if(dialog.type()==='prompt')await dialog.accept('echo');else await dialog.accept();});
 try{
  await page.goto(info.origin+'/?view=settings&section=providers#key='+capability);
  await page.getByRole('textbox',{name:'名称',exact:true}).fill('Fixture '+Date.now());
  const name=await page.getByRole('textbox',{name:'名称',exact:true}).inputValue();
  await page.getByRole('textbox',{name:'服务地址',exact:true}).fill(origin);
  await page.getByLabel('API Key',{exact:false}).fill('synthetic-draft-not-a-real-key');
  await page.getByRole('textbox',{name:'备注',exact:true}).fill('中文草稿不会因切页或焦点变化而丢失。');
  await page.getByRole('button',{name:'上下文',exact:true}).click();
  await page.getByRole('button',{name:'模型与 Key',exact:true}).click();
  checks.page_draft=await page.getByRole('textbox',{name:'名称',exact:true}).inputValue()===name;
  checks.secret_memory_draft=await page.getByLabel('API Key',{exact:false}).inputValue()==='synthetic-draft-not-a-real-key';
  await page.evaluate(()=>{window.dispatchEvent(new Event('blur'));window.dispatchEvent(new Event('focus'));});
  checks.focus_draft=await page.getByRole('textbox',{name:'备注',exact:true}).inputValue()==='中文草稿不会因切页或焦点变化而丢失。';
  await page.getByRole('button',{name:'测试连接',exact:true}).click();
  await page.getByRole('status').filter({hasText:'连接成功'}).waitFor();
  checks.test_keeps_form=await page.getByRole('textbox',{name:'名称',exact:true}).inputValue()===name;
  checks.model_names=await page.getByText('fixture-short',{exact:true}).count()===1&&await page.getByText('fixture-long',{exact:true}).count()===1;
  checks.secret_not_in_storage=await page.evaluate(()=>![...Object.values(localStorage),...Object.values(sessionStorage)].some(value=>value.includes('synthetic-draft-not-a-real-key')));
  await page.getByLabel('API Key',{exact:false}).fill('');await page.getByRole('checkbox',{name:'无需 Key（仅回环服务）',exact:true}).check();
  await page.getByRole('button',{name:'保存配置',exact:true}).click();
  await page.getByRole('switch',{name:'启用此 Provider',exact:true}).waitFor();
  checks.default_off=await page.getByRole('switch',{name:'启用此 Provider',exact:true}).getAttribute('aria-checked')==='false';
  await page.getByRole('switch',{name:'启用此 Provider',exact:true}).click();
  await page.getByRole('button',{name:'自动填充 2 个模型',exact:true}).click();
  await page.locator('.settings-model-list li').filter({hasText:'fixture-short'}).getByRole('button',{name:'设为默认',exact:true}).waitFor();
  checks.auto_fill=await page.locator('.settings-model-list li').count()===2;
  const beforeReasoning=await environment(),shortID=Object.keys(beforeReasoning.models).find(id=>beforeReasoning.models[id].model==='fixture-short'),longID=Object.keys(beforeReasoning.models).find(id=>beforeReasoning.models[id].model==='fixture-long');
  assert(shortID&&longID,'both fixture models exist');
  const originalLong=beforeReasoning.models[longID].capabilities;
  let options=await modelOptions('fixture-short');
  await choose(options,'请求协议','chat');await choose(options,'流式输出','none');await choose(options,'思考协议','DeepSeek 思考协议');
  await options.getByRole('textbox',{name:'声明的等级（逗号分隔；新值可自行声明）',exact:true}).fill('low,high,max');
  await choose(options,'主回答默认思考','思考 · high（未经官方核对）');await choose(options,'压缩 / 扩展分析思考','关闭思考');
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'上下文',exact:true}).click();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();
  options=await modelOptions('fixture-short');
  assert.equal(await options.getByRole('textbox',{name:'声明的等级（逗号分隔；新值可自行声明）',exact:true}).inputValue(),'low,high,max');
  assert.equal((await options.getByRole('combobox',{name:'主回答默认思考',exact:true}).innerText()).trim(),'思考 · high（未经官方核对）');
  assert.equal((await options.getByRole('combobox',{name:'压缩 / 扩展分析思考',exact:true}).innerText()).trim(),'关闭思考');
  checks.reasoning_draft_survives_navigation=true;
  await page.evaluate(()=>{window.dispatchEvent(new Event('blur'));window.dispatchEvent(new Event('focus'));});
  assert.equal((await options.getByRole('combobox',{name:'主回答默认思考',exact:true}).innerText()).trim(),'思考 · high（未经官方核对）');
  assert.deepEqual((await environment()).models[shortID].capabilities,beforeReasoning.models[shortID].capabilities);
  checks.reasoning_draft_is_unapplied=true;
  const expectedReasoning={control:'deepseek',efforts:'low,high,max',default:{effort:'high'},auxiliary:{mode:'disabled'}};
  const reasoningPlanResponse=page.waitForResponse(response=>new URL(response.url()).pathname==='/api/control/plan'&&response.request().postDataJSON()?.kind==='model.set_profile');
  await options.getByRole('button',{name:'预览并保存模型配置',exact:true}).click();
  const reasoningResponse=await reasoningPlanResponse;assert(reasoningResponse.ok(),'reasoning capability preview');
  assert.deepEqual(reasoningResponse.request().postDataJSON().arguments.capabilities.reasoning,expectedReasoning);
  const approval=page.getByRole('region',{name:'待确认操作',exact:true});await approval.waitFor();
  assert((await approval.innerText()).includes('auxiliary')&&(await approval.innerText()).includes('high'));
  assert.deepEqual((await environment()).models[shortID].capabilities,beforeReasoning.models[shortID].capabilities);
  checks.reasoning_preview_exact_and_unapplied=true;
  await approval.getByRole('button',{name:'批准并继续',exact:true}).click();
  await page.getByRole('status').filter({hasText:'模型配置已保存'}).waitFor();
  const savedReasoning=await environment();assert.deepEqual(savedReasoning.models[shortID].capabilities.reasoning,expectedReasoning);assert.deepEqual(savedReasoning.models[longID].capabilities,originalLong);
  checks.reasoning_main_aux_saved_separately=true;checks.reasoning_other_model_unchanged=true;
  await options.getByLabel('上下文窗口（token，0 为自动）',{exact:true}).fill('131072');
  await options.getByRole('button',{name:'预览并保存模型配置',exact:true}).click();
  let savedConfig=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/control/confirm');
  await page.getByRole('region',{name:'待确认操作',exact:true}).getByRole('button',{name:'批准并继续',exact:true}).click();await savedConfig;
  await options.getByRole('button',{name:'预览并保存模型配置',exact:true}).waitFor();
  assert.equal((await environment()).models[shortID].capabilities.context_window_tokens,131072);
  await options.getByLabel('模型输出 token 上限',{exact:true}).fill('1024');
  await options.getByRole('button',{name:'预览并保存模型配置',exact:true}).click();
  savedConfig=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/control/confirm');
  await page.getByRole('region',{name:'待确认操作',exact:true}).getByRole('button',{name:'批准并继续',exact:true}).click();await savedConfig;
  await options.getByRole('button',{name:'预览并保存模型配置',exact:true}).waitFor();
  const sequential=(await environment()).models[shortID].capabilities;
  assert.equal(sequential.context_window_tokens,131072);assert.equal(sequential.output_tokens,1024);assert.deepEqual(sequential.reasoning,expectedReasoning);
  checks.model_window_survives_sequential_saves=true;
  async function externalReasoning(reasoning){
    const operation=await api('control/plan',{kind:'model.set_profile',arguments:{id:shortID,reasoning}});
    const confirmation=await api('control/confirmation?operation_id='+operation.operation_id);
    await api('control/confirm',{operation_id:operation.operation_id,code:confirmation.code});
  }
  await externalReasoning({});
  await options.getByRole('combobox',{name:'思考协议',exact:true}).filter({hasText:'未声明 / 不发送参数'}).waitFor();
  assert.equal(await options.getByRole('combobox',{name:'主回答默认思考',exact:true}).count(),0);
  assert.equal(await options.getByLabel('上下文窗口（token，0 为自动）',{exact:true}).inputValue(),'131072');
  await externalReasoning(expectedReasoning);
  await options.getByRole('combobox',{name:'主回答默认思考',exact:true}).waitFor();
  assert((await options.getByRole('combobox',{name:'主回答默认思考',exact:true}).innerText()).includes('high'));
  checks.clean_model_form_follows_external_clear_and_restore=true;
  await page.reload();await page.getByRole('navigation',{name:'Provider 配置',exact:true}).getByRole('button').filter({hasText:name}).click();options=await modelOptions('fixture-short');
  assert.equal((await options.getByRole('combobox',{name:'思考协议',exact:true}).innerText()).trim(),'DeepSeek 思考协议');
  assert.equal(await options.getByRole('textbox',{name:'声明的等级（逗号分隔；新值可自行声明）',exact:true}).inputValue(),'low,high,max');
  assert.equal((await options.getByRole('combobox',{name:'主回答默认思考',exact:true}).innerText()).trim(),'思考 · high（未经官方核对）');
  assert.equal((await options.getByRole('combobox',{name:'压缩 / 扩展分析思考',exact:true}).innerText()).trim(),'关闭思考');
  checks.reasoning_defaults_survive_reload=true;
  const chat=await api('chats',{title:'Model and reasoning browser acceptance'});
  await page.goto(info.origin+'/?chat='+chat.id);
  const modelChoice=page.getByRole('combobox',{name:'当前模型',exact:true});await modelChoice.waitFor();assert((await modelChoice.innerText()).includes('模型：'));checks.model_selector_is_labelled=true;
  await choose(page,'当前模型','fixture-short');
  await choose(page,'本轮思考','思考 · max（未经官方核对）');
  const composer=page.getByRole('textbox',{name:'消息',exact:true});
  await composer.fill('deepseek-max');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Fixture reply: deepseek-max',{exact:true}).waitFor({timeout:15000});
  assert.deepEqual(modelRequests.at(-1).thinking,{type:'enabled'});assert.equal(modelRequests.at(-1).reasoning_effort,'max');checks.deepseek_composer_effort_reaches_wire=true;
  await choose(page,'本轮思考','关闭思考');await composer.fill('deepseek-off');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Fixture reply: deepseek-off',{exact:true}).waitFor({timeout:15000});assert.deepEqual(modelRequests.at(-1).thinking,{type:'disabled'});assert.equal(Object.hasOwn(modelRequests.at(-1),'reasoning_effort'),false);checks.deepseek_composer_toggle_reaches_wire=true;
  await choose(page,'当前模型','fixture-long');const configure=page.getByRole('button',{name:'配置当前模型的思考能力',exact:true});await configure.waitFor();await configure.click();await page.getByRole('navigation',{name:'Provider 配置',exact:true}).getByRole('button').filter({hasText:name}).click();checks.undeclared_reasoning_has_settings_entry=true;
  options=await modelOptions('fixture-long');await choose(options,'请求协议','chat');await choose(options,'流式输出','none');await choose(options,'思考协议','OpenAI 思考等级');await options.getByRole('textbox',{name:'声明的等级（逗号分隔；新值可自行声明）',exact:true}).fill('low,high');await options.getByRole('button',{name:'预览并保存模型配置',exact:true}).click();await page.getByRole('region',{name:'待确认操作',exact:true}).getByRole('button',{name:'批准并继续',exact:true}).click();await page.getByRole('status').filter({hasText:'模型配置已保存'}).waitFor();
  await page.goto(info.origin+'/?chat='+chat.id);await choose(page,'当前模型','fixture-long');await page.getByRole('combobox',{name:'本轮思考',exact:true}).click();assert.equal(await page.getByRole('option',{name:'关闭思考',exact:true}).count(),0);assert.equal(await page.getByRole('option',{name:'开启思考（默认强度）',exact:true}).count(),0);await page.getByRole('option',{name:'思考 · low（未经官方核对）',exact:true}).click();
  await composer.fill('openai-low');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Fixture reply: openai-low',{exact:true}).waitFor({timeout:15000});assert.equal(modelRequests.at(-1).reasoning_effort,'low');assert.equal(Object.hasOwn(modelRequests.at(-1),'thinking'),false);checks.openai_composer_uses_declared_efforts=true;
  await choose(page,'本轮思考','思考 · high（未经官方核对）');await choose(page,'当前模型','fixture-short');await page.waitForFunction(()=>document.querySelector('[role="combobox"][aria-label="本轮思考"]')?.textContent.includes('继承模型设置'));checks.model_switch_resets_reasoning_override=true;
  await page.setViewportSize({width:390,height:844});assert(await modelChoice.isVisible());assert(await page.getByRole('combobox',{name:'本轮思考',exact:true}).isVisible());assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1));checks.narrow_model_reasoning_controls=true;await page.setViewportSize({width:1440,height:1024});
  await page.goto(info.origin+'/?view=settings&section=providers');await page.getByRole('navigation',{name:'Provider 配置',exact:true}).getByRole('button').filter({hasText:name}).click();
  const short=page.locator('.settings-model-list li').filter({hasText:'fixture-short'});
  await short.getByRole('button',{name:'设为默认',exact:true}).click();
  await short.getByText(/默认模型/).waitFor();checks.set_default=await short.getByRole('button',{name:'设为默认',exact:true}).isDisabled();
  await page.getByRole('button',{name:'删除模型 fixture-short',exact:true}).click();
  await page.getByRole('button',{name:'删除模型 fixture-short',exact:true}).waitFor({state:'hidden'});checks.delete_default=true;
  await page.getByRole('button',{name:'删除 Provider',exact:true}).click();
  await page.getByRole('button',{name:'保存配置',exact:true}).waitFor();
  checks.delete_provider=true;
  if(process.env.FREEAGENT_UI_GALLERY==='1'){
  await page.goto(info.origin+'/?view=gallery');
  await page.getByRole('heading',{name:'FreeAgent 组件状态',exact:true}).waitFor();
  await page.getByRole('button',{name:'批准并继续',exact:true}).first().click();
  await page.getByRole('status').filter({hasText:'已批准'}).waitFor();checks.gallery_real_components=true;
  }
  checks.no_runtime_errors=errors.length===0;
  await page.screenshot({path:path.join(artifacts,'freeagent-v81-gallery.png')});
  fs.writeFileSync(path.join(artifacts,'v81-provider-results.json'),JSON.stringify({checks,errors},null,2));console.log(JSON.stringify({checks,errors}));
  if(Object.values(checks).some(value=>!value))throw new Error('provider browser checks failed');
 }catch(error){console.log(JSON.stringify({checks,errors}));await page.screenshot({path:path.join(artifacts,'freeagent-v81-provider-failure.png')});throw error;}
 finally{await browser.close();await new Promise(resolve=>modelServer.close(resolve));}
})().catch(error=>{console.error(error.message);process.exitCode=1;});
