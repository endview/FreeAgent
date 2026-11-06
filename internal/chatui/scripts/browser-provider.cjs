const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http');
const {chromium}=require('playwright');
const artifacts=process.env.FREEAGENT_UI_ARTIFACTS;
if(!artifacts||!process.env.FREEAGENT_UI_TEST_ROOT)throw new Error('Use scripts/test-frontend.py with an isolated Core.');
fs.mkdirSync(artifacts,{recursive:true});
(async()=>{
 const info=JSON.parse(fs.readFileSync(path.join(process.env.FREEAGENT_UI_TEST_ROOT,'data/engine.json'),'utf8'));
 const capability=fs.readFileSync(path.join(process.env.FREEAGENT_UI_TEST_ROOT,'data/chat.secret'),'utf8').trim();
 const modelServer=http.createServer((req,res)=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify({data:[{id:'fixture-short'},{id:'fixture-long'}]}));});
 await new Promise(resolve=>modelServer.listen(0,'127.0.0.1',resolve));
 const origin='http://127.0.0.1:'+modelServer.address().port+'/v1';
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:1024}});const errors=[],checks={};
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
