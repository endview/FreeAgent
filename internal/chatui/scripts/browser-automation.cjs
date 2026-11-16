// Real UI + isolated Core. No production account, real model, or desktop input.
const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS,packages=process.env.FREEAGENT_AUTOMATION_PACKAGES;
 assert(root&&out&&packages,'Use test-frontend.py and built optional packages');fs.mkdirSync(out,{recursive:true});
 const info=JSON.parse(fs.readFileSync(path.join(root,'data','engine.json'),'utf8')),key=fs.readFileSync(path.join(root,'data','chat.secret'),'utf8').trim();
 async function api(route,body){const r=await fetch(info.origin+'/api/'+route,{method:body?'POST':'GET',headers:{'Origin':info.origin,'X-FreeAgent-Key':key,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const v=await r.json();assert(r.ok,JSON.stringify(v));return v;}
 await api('extensions',{path:path.join(packages,'cc.endview.browser')});
 const browser=await chromium.launch({headless:true,channel:process.env.FREEAGENT_BROWSER_CHANNEL||'chrome'}),page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[];page.on('pageerror',e=>errors.push(e.message));
 try{
  await page.goto(info.origin+'/?view=settings&section=extensions#key='+key);await page.getByRole('button',{name:/浏览器 Browser/}).click();
  await page.getByRole('textbox',{name:'浏览器程序路径',exact:true}).fill('C:\\test\\chrome.exe');
  await page.getByRole('textbox',{name:'允许的网站来源（每行一个）',exact:true}).fill('https://example.org\nhttps://go.dev');
  await page.getByRole('textbox',{name:'浏览器程序路径',exact:true}).focus();
  const save=()=>page.getByRole('button',{name:'保存扩展默认设置',exact:true}).click();
  await save();await page.getByText('扩展配置已保存。',{exact:true}).waitFor();
  let saved=(await api('extensions')).extensions.find(e=>e.id==='cc.endview.browser');assert.equal(saved.settings.executable,'C:\\test\\chrome.exe');assert.deepEqual(saved.settings.allowed_origins,['https://example.org','https://go.dev']);assert.equal(saved.enabled,false);
  await page.reload();await page.getByRole('button',{name:/浏览器 Browser/}).click();assert.equal(await page.getByRole('textbox',{name:'浏览器程序路径',exact:true}).inputValue(),'C:\\test\\chrome.exe');
  for(const [width,height]of [[1440,960],[1024,768],[768,1024],[390,844]]){await page.setViewportSize({width,height});assert(!(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1)),'horizontal overflow '+width);await page.screenshot({path:path.join(out,'automation-'+width+'.png'),fullPage:true});}
  assert.deepEqual(errors,[]);const checks={simple_settings_save_readback:true,disabled_extension_not_started:true,responsive_widths:[1440,1024,768,390],page_errors:errors};fs.writeFileSync(path.join(out,'automation-checks.json'),JSON.stringify(checks,null,2));console.log(JSON.stringify(checks));
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
