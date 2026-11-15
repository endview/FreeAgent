// Production bundle, isolated Core, synthetic model responses and explicit
// long-chain UI fixtures. No real Provider, channel, or user data is involved.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!out)throw Error('Use scripts/test-frontend.py with an isolated Core.');
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')),key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
 const checks={},errors=[],tag=Date.now().toString();
 const fixture=http.createServer((req,res)=>{let raw='';req.on('data',chunk=>raw+=chunk);req.on('end',()=>{
  const body=JSON.parse(raw),answer='## 部署说明已整理\n\n先备份数据，再替换程序。检查服务状态后，核对渠道与网页回复。\n\n- **首次启动**：准备环境，确认服务就绪。\n- **版本升级**：保留备份，逐项验证。\n- **常见排障**：检查连接、端口和执行记录。';
  res.setHeader('Content-Type','text/event-stream');res.end('data: '+JSON.stringify({model:'wire-'+body.model,choices:[{index:0,delta:{content:answer},finish_reason:null}]})+'\n\ndata: '+JSON.stringify({model:'wire-'+body.model,choices:[{index:0,delta:{},finish_reason:'stop'}],usage:{prompt_tokens:200,completion_tokens:60,total_tokens:260}})+'\n\ndata: [DONE]\n\n');
 });});await new Promise(resolve=>fixture.listen(0,'127.0.0.1',resolve));
 const headers={'X-FreeAgent-Key':key,Origin:info.origin,'Content-Type':'application/json'};
 async function api(route,body,method){const res=await fetch(info.origin+'/api/'+route,{method:method||(body?'POST':'GET'),headers,body:body?JSON.stringify(body):undefined});const result=await res.json();assert(res.ok,route+': '+(result.error||res.status));return result;}
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}),page=await browser.newPage({viewport:{width:1440,height:960}});page.on('pageerror',e=>errors.push(e.message));
 try{
  const profile=await api('keys',{name:'UI models '+tag,protocol:'openai-compatible',base_url:'http://127.0.0.1:'+fixture.address().port+'/v1',source:'none'});
  await api('providers',{id:profile.id,enabled:true},'PUT');
  for(const name of ['a','b'])await api('models/enable',{key:profile.id,model:'sample-'+name,id:'ui-'+name+'-'+tag,default:name==='a'});
  const chat=await api('chats',{title:'部署说明整理 · 多模型接续'});
  await page.goto(info.origin+'/?chat='+chat.id+'#key='+key);
  const input=page.getByRole('textbox',{name:'消息',exact:true});
  async function choose(name){await page.getByRole('combobox',{name:'当前模型',exact:true}).click();await page.getByRole('option',{name,exact:true}).click();}
  await input.fill('把服务器部署流程整理成一份说明，保留启动、升级与排障。');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.locator('.turn-model').filter({hasText:'wire-sample-a'}).waitFor();
  await choose('sample-b');assert.equal(await page.locator('.turn-model').first().innerText(),'wire-sample-a');checks.selector_keeps_historical_model=true;
  await input.fill('换一个模型，再核对一遍升级顺序。');await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.locator('.turn-model').filter({hasText:'wire-sample-b'}).waitFor();
  await choose('sample-a');assert.deepEqual(await page.locator('.turn-model').allTextContents(),['wire-sample-a','wire-sample-b']);checks.real_core_preserves_each_generating_model=true;
  await page.locator('.turn-model').first().click();assert.equal(await page.locator('.turn-overview [data-turn-model]').innerText(),'wire-sample-a');checks.selected_round_keeps_its_configuration=true;
  await page.getByRole('button',{name:'本轮能力',exact:true}).click();assert.equal(await page.locator('.capability-group').count(),4);checks.all_four_capability_groups=true;await page.keyboard.press('Escape');
  await page.screenshot({path:path.join(out,'workbench-multiple-models.png')});
  const snapshot=await api('chats/'+chat.id),turn=snapshot.view.turns.at(-1);
  let rows=Array.from({length:270},(_,i)=>({key:'synthetic-'+i,sequence:i+1,kind:'tool',name:i%2?'file_read':'command_run',status:i>=10&&i<19?['waiting_confirmation','unknown','failed'][i%3]:'finished',text:'合成执行结果 '+i,duration_ms:300+i,target:i===269?'C:/very/long/'+('中文无空格路径'.repeat(60)):'/workspace/example.md'}));
  const buildSnapshot=()=>{const value=structuredClone(snapshot),t=value.view.turns.at(-1);t.activity=rows.slice(-128);t.activity_has_earlier=true;const pending=rows.filter(item=>['waiting_confirmation','unknown','failed'].includes(item.status));t.attention_activity=pending.slice(-3);t.attention_count=pending.length;return value;};
  await page.route('**/api/chats/'+chat.id,route=>route.fulfill({json:buildSnapshot()}));
  await page.route('**/api/chats/'+chat.id+'/activity?*',route=>{const before=new URL(route.request().url()).searchParams.get('before'),limit=before?rows.findIndex(r=>r.key===before):rows.length,end=limit<0?rows.length:limit,start=Math.max(0,end-128),items=rows.slice(start,end);return route.fulfill({json:{run_id:turn.key,items,before:items[0]?.key,total:rows.length,has_earlier:start>0}});});
  await page.reload();await page.locator('.turn-overview .recent-activities li').nth(2).waitFor();
  assert.deepEqual(await page.locator('.turn-overview .recent-activities li').evaluateAll(es=>es.map(e=>e.dataset.activity)),['synthetic-18','synthetic-17','synthetic-16']);checks.three_priority_rows_include_older_unresolved=true;
  assert.equal(await page.locator('.recent-activities small,.recent-activities p').count(),0);checks.flow_has_no_explanatory_sublines=true;
  await input.fill('实时更新时保留草稿与焦点');await input.focus();rows[269]={...rows[269],status:'unknown',name:'very_long_operation_'+('无空格名称'.repeat(45))};
  await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));
  await page.waitForFunction(()=>document.querySelector('.turn-overview .recent-activities li')?.dataset.activity==='synthetic-269');
  assert.equal(await input.inputValue(),'实时更新时保留草稿与焦点');assert(await input.evaluate(e=>document.activeElement===e));assert(await page.locator('.turn-overview').evaluate(e=>e.scrollWidth<=e.clientWidth+1));checks.live_priority_preserves_input_focus=true;

  await page.locator('.turn-overview .expand-chain').click();await page.getByRole('dialog',{name:'完整执行链条'}).waitFor();
  await page.locator('.chain-scroll').evaluate(e=>e.scrollTop=90);const chainAnchor=await page.locator('.chain-scroll').evaluate(e=>{const y=e.getBoundingClientRect().top,row=[...e.querySelectorAll('[data-activity]')].find(r=>r.getBoundingClientRect().bottom>y);return {key:row.dataset.activity,y:row.getBoundingClientRect().top};});
  rows[269]={...rows[269],status:'finished'};await page.evaluate(()=>document.dispatchEvent(new Event('visibilitychange')));await page.waitForFunction(()=>document.querySelector('.turn-overview .recent-activities li')?.dataset.activity==='synthetic-18');
  assert(Math.abs((await page.locator('[data-activity="'+chainAnchor.key+'"]').last().boundingBox()).y-chainAnchor.y)<2);checks.live_chain_preserves_reading_anchor=true;

  await page.getByRole('button',{name:'加载更早记录',exact:true}).click();await page.locator('.chain-record').nth(255).waitFor();await page.getByRole('button',{name:'加载更早记录',exact:true}).click();await page.locator('.chain-record').nth(269).waitFor();assert.equal(await page.locator('.chain-record').count(),270);checks.full_chain_pages_without_loss=true;
  await page.getByRole('button',{name:/^待处理 /}).click();assert.equal(await page.locator('.chain-record').count(),9);checks.all_attention_records_remain_accessible=true;await page.screenshot({path:path.join(out,'workbench-attention-chain.png')});await page.keyboard.press('Escape');
  checks.chain_returns_keyboard_focus=await page.locator('.turn-overview .expand-chain').evaluate(e=>e===document.activeElement);
  for(const [width,height] of [[1920,1080],[1440,960],[1024,768],[768,1024],[390,844],[320,568],[844,390]]){
   await page.setViewportSize({width,height});await input.fill('正在编辑的草稿');
   if(width<1180)await page.getByRole('button',{name:'本轮详情',exact:true}).click();
   const overview=width<1180?page.locator('.overview-dialog'):page.locator('.turn-overview');assert.equal(await overview.locator('.recent-activities li').count(),3);await page.screenshot({path:path.join(out,`workbench-overview-${width}x${height}.png`)});
   await overview.locator('.expand-chain').click();const modal=page.locator('.execution-chain-dialog');await modal.waitFor();assert(await modal.evaluate(e=>{const r=e.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth+1&&r.bottom<=innerHeight+1&&e.scrollWidth<=e.clientWidth+1;}));await page.keyboard.press('Escape');assert.equal(await input.inputValue(),'正在编辑的草稿');assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  }
  checks.responsive_overview_chain_and_draft=true;assert.deepEqual(errors,[]);assert(Object.values(checks).every(Boolean));fs.writeFileSync(path.join(out,'workbench-results.json'),JSON.stringify({checks,errors,long_chain_data:'synthetic UI fixture; paging scope separately tested with real PG'},null,2));console.log(JSON.stringify({checks,errors}));
 }catch(e){await page.screenshot({path:path.join(out,'workbench-failure.png')});fs.writeFileSync(path.join(out,'workbench-partial.json'),JSON.stringify({checks,errors},null,2));throw e;}
 finally{await browser.close();await new Promise(resolve=>fixture.close(resolve));}
})().catch(e=>{console.error(e);process.exitCode=1});
