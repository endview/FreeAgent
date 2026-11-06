const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!out)throw new Error('Use the isolated frontend benchmark runner.');fs.mkdirSync(out,{recursive:true});
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')),key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim(),report={synthetic:true,conversations:10000,turns:5000,api:{}};
 const headers={'X-FreeAgent-Key':key,'Origin':info.origin,'Content-Type':'application/json'};
 async function api(route,body,method){const result=await fetch(info.origin+'/api/'+route,{headers,method:method||(body?'POST':'GET'),body:body?JSON.stringify(body):undefined});if(!result.ok)throw new Error(route+' '+result.status);return result.json();}
 const percentile=(values,p)=>[...values].sort((a,b)=>a-b)[Math.floor((values.length-1)*p)];
 for(const [name,route] of Object.entries({list:'chats?page=1&limit=60&space_id=local',search:'chats?page=1&limit=60&space_id=local&q='+encodeURIComponent('基准会话 1'),history:'chats/ui-bench-00001'})){
  const samples=[];let bytes=0;for(let n=0;n<205;n++){const start=performance.now(),result=await api(route);const elapsed=performance.now()-start;if(n>=5)samples.push(elapsed);bytes=JSON.stringify(result).length;}
  report.api[name]={samples:200,p50_ms:percentile(samples,.5),p95_ms:percentile(samples,.95),response_chars:bytes};
 }
 if(process.env.FREEAGENT_UI_API_ONLY==='1'){fs.writeFileSync(path.join(out,'api-performance.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));return;}
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}});
 const errors=[];page.on('pageerror',error=>errors.push(error.message));let eventRequests=0;page.on('request',r=>{if(r.url().endsWith('/api/events'))eventRequests++;});
 let fixture,timer;
 try{
  fs.writeFileSync(path.join(out,'performance-progress.json'),JSON.stringify(report,null,2));const start=performance.now();await page.goto(info.origin+'/?chat=ui-bench-00001#key='+key);const input=page.getByRole('textbox',{name:'消息',exact:true});await input.waitFor();await page.locator('.turn').last().waitFor();report.open_page_ms=performance.now()-start;
  const load=performance.now();while(await page.locator('.turn').count()<200){await page.getByRole('button',{name:'加载更早的消息',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('.load-earlier:disabled'));}report.expand_200_turns_ms=performance.now()-load;report.rendered_turns=await page.locator('.turn').count();
  const interactive=performance.now();await input.fill('打开长历史后仍可输入');report.input_after_200_ms=performance.now()-interactive;
  await page.getByRole('button',{name:'新对话',exact:true}).click();
  let fragments=0;
  fixture=http.createServer((req,res)=>{let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{
   res.setHeader('Content-Type','text/event-stream');let n=0;const began=performance.now();const currentTimer=setInterval(()=>{const target=Math.min(2700,Math.floor((performance.now()-began)*30/1000));while(n<target){fragments++;res.write('data: '+JSON.stringify({choices:[{index:0,delta:{content:'流式基准 '+(++n)+'：保持中文、代码与公式。\n\n'},finish_reason:null}]})+'\n\n');if(n===2700){clearInterval(currentTimer);res.write('data: '+JSON.stringify({choices:[{index:0,delta:{},finish_reason:'stop'}],usage:{prompt_tokens:50,completion_tokens:1000,total_tokens:1050}})+'\n\ndata: [DONE]\n\n');res.end();break;}}},10);timer=currentTimer;res.on('close',()=>clearInterval(currentTimer));
  });});await new Promise(resolve=>fixture.listen(0,'127.0.0.1',resolve));
  const profile=await api('keys',{name:'Synthetic stream benchmark',protocol:'openai-compatible',base_url:'http://127.0.0.1:'+fixture.address().port+'/v1',source:'none'});await api('providers',{id:profile.id,enabled:true},'PUT');await api('models/enable',{key:profile.id,model:'fixture',id:'stream-benchmark',default:true});
  await page.reload();await input.waitFor();await input.fill('运行流式基准');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByRole('button',{name:'停止运行',exact:true}).waitFor();
  let segments=1;const samples=[],memory=[];const cdp=await page.context().newCDPSession(page);const duration=Number(process.env.FREEAGENT_STREAM_SECONDS||600),streamStart=performance.now();
  await page.evaluate(()=>{const el=document.querySelector('textarea[aria-label="消息"]');window.__keyTimes=[];el.addEventListener('keydown',()=>{const started=performance.now();requestAnimationFrame(()=>window.__keyTimes.push(performance.now()-started));});});
  while(performance.now()-streamStart<duration*1000){if(!await page.getByRole('button',{name:'停止运行',exact:true}).count()){await input.fill('继续流式基准 '+(++segments));await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByRole('button',{name:'停止运行',exact:true}).waitFor();}await input.press('x');await page.waitForTimeout(1000);if(samples.length%30===0){const value=await cdp.send('Runtime.getHeapUsage');memory.push({seconds:Math.round((performance.now()-streamStart)/1000),heap_bytes:value.usedSize});}samples.push(1);if(samples.length%60===0)console.log(JSON.stringify({stream_seconds:Math.round((performance.now()-streamStart)/1000),fragments}));}
  const timings=await page.evaluate(()=>window.__keyTimes);const stopped=performance.now();await page.getByRole('button',{name:'停止运行',exact:true}).click();await page.getByRole('button',{name:'停止运行',exact:true}).waitFor({state:'hidden'});
  report.stream={duration_seconds:duration,actual_seconds:(performance.now()-streamStart)/1000,segments,segment_limit_seconds:90,fragments,key_samples:timings.length,input_p50_ms:percentile(timings,.5),input_p95_ms:percentile(timings,.95),stop_ms:performance.now()-stopped,memory};report.event_requests=eventRequests;report.errors=errors;
  fs.writeFileSync(path.join(out,'performance.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));assert.equal(errors.length,0);assert(report.stream.input_p95_ms<50);assert(report.input_after_200_ms<1000);
 }catch(error){report.error=error.message;report.errors=errors;try{report.owner=await api('attach/state');}catch{}fs.writeFileSync(path.join(out,'performance-failure.json'),JSON.stringify(report,null,2));throw error;}finally{clearInterval(timer);await browser.close();if(fixture)await new Promise(resolve=>fixture.close(resolve));}
})().catch(error=>{console.error(error.message);process.exitCode=1;});
