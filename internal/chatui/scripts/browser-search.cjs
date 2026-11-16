// Real UI and Core, isolated PostgreSQL and a local search API fixture. No
// production Owner, external credentials, or paid calls are used.
const {openSettingsPage}=require('./settings-navigation.cjs');
const {chromium}=require('playwright');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const assert=require('node:assert/strict');
(async()=>{
 const artifacts=process.env.FREEAGENT_UI_ARTIFACTS,root=process.env.FREEAGENT_UI_TEST_ROOT;
 if(!artifacts||!root)throw Error('Use scripts/test-frontend.py with an isolated Core.');
 fs.mkdirSync(artifacts,{recursive:true});
 const info=JSON.parse(fs.readFileSync(path.join(root,'data','engine.json'),'utf8'));
 const key=fs.readFileSync(path.join(root,'data','chat.secret'),'utf8').trim();
 const checks={},errors=[];let calls=0;
 const search=http.createServer((req,res)=>{calls++;const u=new URL(req.url,'http://fixture');assert.equal(u.pathname,'/search');assert.equal(u.searchParams.get('format'),'json');assert.equal(u.searchParams.get('q'),'FreeAgent official documentation');res.setHeader('Content-Type','application/json');res.end(JSON.stringify({results:[{title:'Fixture source',url:'https://example.org/source',content:'Synthetic source text'}]}));});
 await new Promise(resolve=>search.listen(0,'127.0.0.1',resolve));
 async function api(route,body,method){const r=await fetch(info.origin+'/api/'+route,{method:method||(body?'POST':'GET'),headers:{'X-FreeAgent-Key':key,'Origin':info.origin,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const value=await r.json();assert(r.ok,route+': '+JSON.stringify(value));return value;}
 const browser=await chromium.launch({headless:true,...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{})});
 const page=await browser.newPage({viewport:{width:1440,height:1024}});page.on('pageerror',e=>errors.push(e.message));
 async function choose(name,option){await page.getByRole('combobox',{name,exact:true}).click();await page.getByRole('option',{name:option,exact:true}).click();}
 async function save(){const pending=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/settings/web-search'&&r.request().method()==='PATCH');await page.getByRole('button',{name:'保存搜索设置',exact:true}).click();return pending;}
 try{
  const savedKey=await api('keys',{name:'Search browser fixture',protocol:'search',base_url:'http://127.0.0.1:'+search.address().port,source:'none'});
  await api('settings/tools',{network:true,network_allowed_origins:[]},'PATCH');
  await page.goto(info.origin+'/?view=settings&section=search#key='+key);
  await page.getByRole('heading',{name:'联网搜索',level:1,exact:true}).waitFor();
  await choose('默认搜索方式','指定搜索 API');await choose('搜索服务','SearXNG（自托管）');await choose('搜索 Key',savedKey.name);
  await page.getByRole('textbox',{name:'连接名称',exact:true}).fill('fixture-search');
  await page.getByRole('button',{name:'添加到草稿',exact:true}).click();
  let saved=await save();assert(saved.ok(),await saved.text());
  let current=await api('settings/web-search');assert.equal(current.config.mode,'api');assert.equal(current.config.default_connection,'fixture-search');checks.save_readback=true;
  const probe=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/settings/web-search/test');await page.getByRole('button',{name:'测试连接',exact:true}).click();assert((await probe).ok());assert.equal(calls,1);checks.explicit_probe_once=true;
  await page.getByText('搜索预算',{exact:true}).click();
  await page.getByRole('spinbutton',{name:'每次最多结果',exact:true}).fill('3');
  await openSettingsPage(page,'通用');await openSettingsPage(page,'联网搜索');
  await page.getByText('搜索预算',{exact:true}).click();assert.equal(await page.getByRole('spinbutton',{name:'每次最多结果',exact:true}).inputValue(),'3');checks.navigation_draft=true;
  saved=await save();assert(saved.ok(),await saved.text());
  current=await api('settings/web-search');
  await page.getByRole('spinbutton',{name:'每次最多结果',exact:true}).fill('4');
  await api('settings/web-search',{config:{...current.config,max_results:2},expected_revisions:current.expected_revisions},'PATCH');
  await openSettingsPage(page,'通用');await openSettingsPage(page,'联网搜索');await page.getByText('搜索预算',{exact:true}).click();
  assert.equal((await save()).status(),409);assert.equal(await page.getByRole('spinbutton',{name:'每次最多结果',exact:true}).inputValue(),'4');checks.conflict_retains_draft=true;
  await page.reload();await page.getByRole('button',{name:'保存搜索设置',exact:true}).waitFor();
  for(const [width,height]of [[1440,1024],[1024,768],[768,1024],[390,844]]){
   await page.setViewportSize({width,height});await page.waitForTimeout(100);
   const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>document.documentElement.clientWidth+1);assert(!overflow,'horizontal overflow at '+width);
   if(width<=760)assert(!(await page.locator('.settings-nav').isVisible()),'mobile detail must hide navigation');
   await page.screenshot({path:path.join(artifacts,`search-${width}.png`),fullPage:true});
  }
  checks.responsive=true;assert.deepEqual(errors,[]);checks.page_errors=errors;
  fs.writeFileSync(path.join(artifacts,'search-checks.json'),JSON.stringify(checks,null,2));console.log(JSON.stringify(checks));
 } finally {await browser.close();await new Promise(resolve=>search.close(resolve));}
})().catch(e=>{console.error(e);process.exitCode=1;});
