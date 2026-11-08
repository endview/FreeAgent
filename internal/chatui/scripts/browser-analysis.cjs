// Real production React assets and one isolated Core; no real Provider or Bot.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!out)throw Error('Use the isolated frontend runner.');
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8'));
 const key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
 const headers={'X-FreeAgent-Key':key,Origin:info.origin,'Content-Type':'application/json'};
 async function api(route,body){const response=await fetch(info.origin+'/api/'+route,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body)});const value=await response.json();assert(response.ok,route+': '+(value.error||response.status));return value;}
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:1000}}),checks={},errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 try{
  const chat=await api('chats',{title:'Explicit analysis boundary'});
  await page.goto(info.origin+'/?chat='+chat.id+'#key='+key);
  const input=page.getByRole('textbox',{name:'消息',exact:true});await input.fill('Source record for explicit analysis.');
  await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: Source record for explicit analysis.',{exact:true}).waitFor();
  assert.equal(await page.getByText('使用 Skill 分析本轮',{exact:true}).count(),0);checks.no_uninstalled_action=true;
  const skill=path.resolve(__dirname,'../../../optional-skills/experience-summary');
  await api('skills',{path:skill});await page.reload();await input.waitFor();
  assert.equal(await page.getByText('使用 Skill 分析本轮',{exact:true}).count(),0);checks.disabled_method_no_action=true;
  await api('skills/experience-summary/update',{enabled:true});await page.reload();
  await page.getByText('使用 Skill 分析本轮',{exact:true}).first().click();
  await page.getByRole('button',{name:'预览本轮来源',exact:true}).first().click();
  await page.getByRole('button',{name:'确认来源，开始分析',exact:true}).waitFor();checks.preview_before_start=true;
  await page.getByRole('button',{name:'确认来源，开始分析',exact:true}).click();
  const draft=page.getByText('分析草稿与保存',{exact:true});await draft.waitFor({timeout:15000});await draft.click();
  await page.getByRole('combobox',{name:'草稿类型',exact:true}).click();await page.getByRole('option',{name:'简短 Memory',exact:true}).click();
  await page.getByRole('textbox',{name:'标题',exact:true}).fill('Reviewed explicit record');
  await page.getByRole('textbox',{name:'正文与局限',exact:true}).fill('Synthetic verified convention; this is not a real deployment claim.');
  await page.getByRole('button',{name:'确认保存上述内容到私有 Memory',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Memory 已保存'}).waitFor();checks.explicit_memory_save=true;
  await api('skills/experience-summary/update',{enabled:false});await page.reload();
  await page.getByText('分析草稿与保存',{exact:true}).waitFor();assert.equal(await page.getByText('使用 Skill 分析本轮',{exact:true}).count(),0);checks.disabled_keeps_history=true;
  // A different package uses the same entry without an experience identifier.
  const other=path.join(root,'incident-review');fs.mkdirSync(other);
  fs.writeFileSync(path.join(other,'SKILL.md'),'---\nname: incident-review\ndescription: Inspect selected incident records\ntrigger: manual\ninvocation: selected_sources\n---\nReport only facts and unknown outcomes.\n');
  await api('skills',{path:other});await api('skills/incident-review/update',{enabled:true});await page.reload();
  await page.getByText('使用 Skill 分析本轮',{exact:true}).first().click();
  await page.getByRole('combobox',{name:'分析方法',exact:true}).first().click();await page.getByRole('option',{name:'Inspect selected incident records',exact:true}).waitFor();await page.keyboard.press('Escape');checks.other_method_discovered=true;
  await page.screenshot({path:path.join(out,'analysis-boundaries.png'),fullPage:true});
  checks.no_runtime_errors=errors.length===0;
  fs.writeFileSync(path.join(out,'analysis-results.json'),JSON.stringify({checks,errors},null,2));console.log(JSON.stringify({checks,errors}));assert(Object.values(checks).every(Boolean));
 }finally{await browser.close();}
})().catch(error=>{console.error(error.message);process.exitCode=1;});
