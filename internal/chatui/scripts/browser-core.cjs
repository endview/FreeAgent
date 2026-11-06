const fs=require('node:fs');const path=require('node:path');const os=require('node:os');
const {chromium}=require('playwright');
const artifacts=process.env.FREEAGENT_UI_ARTIFACTS;
if(!artifacts||!process.env.FREEAGENT_UI_TEST_ROOT)throw new Error('Use scripts/test-frontend.py with an isolated Core.');
fs.mkdirSync(artifacts,{recursive:true});
(async()=>{
 const base=path.join(process.env.FREEAGENT_UI_TEST_ROOT,'data');const info=JSON.parse(fs.readFileSync(path.join(base,'engine.json'),'utf8'));const key=fs.readFileSync(path.join(base,'chat.secret'),'utf8').trim();
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true});const page=await browser.newPage({viewport:{width:1440,height:1024}});const errors=[];page.on('pageerror',error=>errors.push(error.message));let eventRequests=0;page.on('request',request=>{if(request.url().endsWith('/api/events'))eventRequests++;});
 const checks={};const prompt="FREEAGENT_REACT_CORE_"+Date.now();
 try{
  const response=await page.goto(info.origin+'/#key='+key);
  checks.production_csp=response.headers()['content-security-policy'].includes("script-src 'self'");
  await page.getByRole('button',{name:'新对话',exact:true}).waitFor();
  await page.getByRole('combobox',{name:'当前模型',exact:true}).waitFor();
  checks.capability_removed=!page.url().includes('key=');
  const input=page.getByRole('textbox',{name:'消息',exact:true});await input.fill(prompt);await page.getByRole('button',{name:'发送消息',exact:true}).click();
  await page.getByText('Echo: '+prompt,{exact:true}).waitFor({timeout:15000});
  checks.echo_round_trip=true;checks.message_count=await page.getByText('Echo: '+prompt,{exact:true}).count()===1;
  await page.getByRole('button',{name:'设置',exact:true}).click();
  await page.getByRole('button',{name:prompt,exact:true}).click();
  checks.one_global_stream=eventRequests===1;
  await page.reload();await page.getByText('Echo: '+prompt,{exact:true}).waitFor({timeout:15000});
  checks.refresh_keeps_local_authorization=true;
  checks.no_runtime_errors=errors.length===0;
  await page.screenshot({path:path.join(artifacts,'freeagent-v81-real-core.png')});
  fs.writeFileSync(path.join(artifacts,'v81-core-results.json'),JSON.stringify({checks,errors},null,2));
  console.log(JSON.stringify({checks,errors}));
  if(Object.values(checks).some(value=>value!==true))throw new Error('Core browser checks failed');
 } finally {await browser.close();}
})().catch(error=>{console.error(error.message);process.exitCode=1;});
