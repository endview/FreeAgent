const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
const artifacts=process.env.FREEAGENT_UI_ARTIFACTS;
if(!artifacts||!process.env.FREEAGENT_UI_TEST_ROOT)throw new Error('Use scripts/test-frontend.py with an isolated Core.');
fs.mkdirSync(artifacts,{recursive:true});
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,info=JSON.parse(fs.readFileSync(root+'/data/engine.json','utf8')),key=fs.readFileSync(root+'/data/chat.secret','utf8').trim();
 const checks={},errors=[],apiFailures=[];let modelCalls=0;const tag=Date.now().toString();
 const timers=new Set();
 const fixture=http.createServer((req,res)=>{let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{
  if(req.url.endsWith('/models')){res.setHeader('Content-Type','application/json');res.end(JSON.stringify({data:[{id:'fixture'}]}));return;}
  const body=JSON.parse(raw);modelCalls++;
  const messages=body.messages||[],last=messages.at(-1),user=[...messages].reverse().find(message=>message.role==='user');const text=(typeof user?.content==='string'?user.content:'').split('[User-provided attachment:')[0].trim();
  let answer={role:'assistant',content:'FIXTURE_REPLY: '+text},calls;
  if(text.startsWith('WRITE ')&&last?.role!=='tool'){const name=text.slice(6).trim();answer={role:'assistant',content:null,tool_calls:[{index:0,id:'call_'+tag,type:'function',function:{name:'file_create',arguments:JSON.stringify({path:name,content:'CREATED_ONCE_'+tag})}}]};}
  if(text==='CODE_'+tag)answer={role:'assistant',content:'```javascript\nconsole.log("synthetic");\n```\n\n公式 $x^2$。'};
  if(last?.role==='tool')answer={role:'assistant',content:'TOOL_RESULT_RETAINED_'+tag};
  const finish=answer.tool_calls?'tool_calls':'stop';
  if(body.stream){
   res.setHeader('Content-Type','text/event-stream');
   const emit=value=>res.write('data: '+JSON.stringify(value)+'\n\n');
   const end=()=>{emit({choices:[{index:0,delta:{},finish_reason:finish}],usage:{prompt_tokens:50,completion_tokens:20,total_tokens:70}});res.end('data: [DONE]\n\n');};
   if(text==='LONG_OPERATION_'+tag){let n=0;const timer=setInterval(()=>{emit({choices:[{index:0,delta:{content:'进度 '+(++n)+'\n'},finish_reason:null}]});if(n===24){clearInterval(timer);timers.delete(timer);end();}},250);timers.add(timer);res.on('close',()=>{clearInterval(timer);timers.delete(timer);});}
   else{emit({choices:[{index:0,delta:answer,finish_reason:null}]});end();}
  }else{res.setHeader('Content-Type','application/json');res.end(JSON.stringify({choices:[{index:0,message:answer,finish_reason:finish}],usage:{prompt_tokens:50,completion_tokens:20,total_tokens:70}}));}
 });});
 await new Promise(resolve=>fixture.listen(0,'127.0.0.1',resolve));
 const headers={'X-FreeAgent-Key':key,'Origin':info.origin,'Content-Type':'application/json'};
 async function api(route,body,method){const res=await fetch(info.origin+'/api/'+route,{method:method||(body?'POST':'GET'),headers,body:body?JSON.stringify(body):undefined});const value=await res.json();if(!res.ok)throw new Error('fixture API '+route+' '+res.status+' '+value.error);return value;}
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true});const page=await browser.newPage({viewport:{width:1440,height:1024}});page.setDefaultTimeout(15000);
 page.on('pageerror',e=>errors.push(e.message));page.on('response',res=>{if(res.url().includes('/api/')&&res.status()>=400)apiFailures.push({path:new URL(res.url()).pathname,status:res.status()});});
 page.on('dialog',async dialog=>{await dialog.accept();});
 let profile;
 try{
  profile=await api('keys',{name:'BrowserTools '+tag,protocol:'openai-compatible',base_url:'http://127.0.0.1:'+fixture.address().port+'/v1',source:'none'});
  await api('providers',{id:profile.id,enabled:true},'PUT');await api('models/enable',{key:profile.id,model:'fixture',id:'browser-fixture-'+tag,default:true});
  await page.goto(info.origin+'/#key='+key);
  const input=page.getByRole('textbox',{name:'消息',exact:true});await input.waitFor();
  await page.getByRole('combobox',{name:'当前权限',exact:true}).click();await page.getByRole('option',{name:'每次询问',exact:true}).click();
  const file='browser-approved-'+tag+'.txt';await input.fill('WRITE '+file);await page.getByRole('button',{name:'发送消息',exact:true}).click();
  await page.getByRole('region',{name:'待确认操作',exact:true}).waitFor();
  checks.exact_preview=(await page.locator('.approval-preview').innerText()).includes(file)&&!fs.existsSync(root+'/workspace/'+file);
  checks.approval_does_not_steal_focus=await input.evaluate(el=>document.activeElement!==document.querySelector('.approval-footer button:last-child'));
  await page.getByRole('button',{name:'批准并继续',exact:true}).click();await page.getByText('TOOL_RESULT_RETAINED_'+tag,{exact:true}).waitFor();
  checks.tool_approval=fs.readFileSync(root+'/workspace/'+file,'utf8')==='CREATED_ONCE_'+tag;
  await page.locator('.tool-summary').first().click();checks.tool_timing=await page.locator('.tool-list time').count()>0;
  const reject='browser-rejected-'+tag+'.txt';await input.fill('WRITE '+reject);await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByRole('button',{name:'拒绝',exact:true}).waitFor();await page.getByRole('button',{name:'拒绝',exact:true}).click();await page.getByRole('button',{name:'拒绝',exact:true}).waitFor({state:'hidden'});checks.decline_no_file=!fs.existsSync(root+'/workspace/'+reject);
  await page.getByRole('button',{name:'上下文',exact:true}).click();await page.getByRole('dialog').getByText('自动压缩已开启',{exact:false}).waitFor();checks.context_inspection=true;await page.keyboard.press('Escape');
  await input.fill('LONG_OPERATION_'+tag);await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByRole('button',{name:'停止运行',exact:true}).waitFor();
  await input.fill('GUIDANCE_'+tag);await page.getByRole('button',{name:'加入队列',exact:true}).click();await page.getByRole('button',{name:'转为引导',exact:true}).waitFor();checks.default_queue=true;
  await page.getByRole('button',{name:'转为引导',exact:true}).click();await page.getByText('FIXTURE_REPLY: GUIDANCE_'+tag,{exact:true}).waitFor({timeout:30000});checks.queue_to_guidance=true;
  await page.getByRole('button',{name:'新对话',exact:true}).click();await input.fill('附件原文_'+tag);
  await page.locator('input[type=file]').setInputFiles({name:'说明.txt',mimeType:'text/plain',buffer:Buffer.from('本地附件合成样本 '+tag)});
  await page.locator('.attachment-drafts').getByText('说明.txt',{exact:true}).waitFor();checks.attachment_keeps_prompt=await input.inputValue()==='附件原文_'+tag;
  await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.locator('.assistant-message').getByText('FIXTURE_REPLY: 附件原文_'+tag,{exact:true}).waitFor();checks.attachment_round_trip=true;
  // Lose only the submit response, after the real Core accepted the original key.
  let lost=false;
  await page.route('**/api/chats/*/submit',async route=>{if(lost)return route.continue();lost=true;await route.fetch();await route.abort('failed');});
  const uncertain='IDEMPOTENCY_'+tag;await input.fill(uncertain);await page.getByRole('button',{name:'发送消息',exact:true}).click();
  await page.getByText('FIXTURE_REPLY: '+uncertain,{exact:true}).waitFor();await page.unroute('**/api/chats/*/submit');
  await page.reload();await page.getByText('FIXTURE_REPLY: '+uncertain,{exact:true}).waitFor();checks.uncertain_draft_survives_reload=await page.getByRole('textbox',{name:'消息',exact:true}).inputValue()===uncertain;
  const before=modelCalls;await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByRole('status').filter({hasText:'原请求已经接收'}).waitFor();checks.uncertain_no_replay=modelCalls===before;
  await page.getByRole('button',{name:'新对话',exact:true}).click();await input.fill('CODE_'+tag);await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByRole('button',{name:'复制代码',exact:true}).waitFor();
  await page.context().grantPermissions(['clipboard-read','clipboard-write']);await page.getByRole('button',{name:'复制代码',exact:true}).click();await page.waitForFunction(async expected=>(await navigator.clipboard.readText()).replace(/\r\n/g,'\n')===expected,'console.log("synthetic");\n');checks.code_copy=true;
  await page.getByRole('button',{name:'复制 LaTeX',exact:true}).click();checks.formula_copy=await page.evaluate(()=>navigator.clipboard.readText())==='x^2';
  const downloadPromise=page.waitForEvent('download');await page.getByRole('button',{name:'另存为 Markdown',exact:true}).click();const exported=await downloadPromise;await exported.saveAs(path.join(artifacts,'synthetic-reply.md'));checks.markdown_download=fs.readFileSync(path.join(artifacts,'synthetic-reply.md'),'utf8').includes('console.log("synthetic");');
  checks.no_runtime_errors=errors.length===0;checks.no_api_failures=apiFailures.length===0;
  await page.screenshot({path:path.join(artifacts,'freeagent-v81-tool-core.png')});
  console.log(JSON.stringify({checks,errors,apiFailures,modelCalls}));fs.writeFileSync(path.join(artifacts,'v81-tool-core-results.json'),JSON.stringify({checks,errors,apiFailures,modelCalls},null,2));
  assert(Object.values(checks).every(Boolean));
 }catch(e){console.log(JSON.stringify({checks,errors,apiFailures,modelCalls}));await page.screenshot({path:path.join(artifacts,'freeagent-v81-tool-core-failure.png')});throw e;}
 finally{await browser.close();try{await api('models/default',{id:'echo'});}catch{};for(const timer of timers)clearInterval(timer);await new Promise(resolve=>fixture.close(resolve));}
})().catch(e=>{console.error(e.message);process.exitCode=1;});
