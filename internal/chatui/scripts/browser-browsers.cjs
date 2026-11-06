const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium,firefox,webkit}=require('playwright');
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!out)throw new Error('Use the isolated frontend acceptance runner.');
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')),key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
 const results=[];
 for(const [name,type] of Object.entries({chromium,firefox,webkit})){
  const browser=await type.launch({...(name==='chromium'&&process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true});
  const page=await browser.newPage({viewport:{width:390,height:844}}),errors=[],checks={};page.on('pageerror',error=>errors.push(error.message));
  try{
   await page.goto(info.origin+'/#key='+key);const input=page.getByRole('textbox',{name:'消息',exact:true});await input.waitFor();
   const prompt=name+'_中文_'+Date.now();await input.fill(prompt);await input.press('Enter');await page.getByText('Echo: '+prompt,{exact:true}).waitFor();checks.round_trip=true;
   checks.no_page_overflow=await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth);
   checks.mobile_font=await input.evaluate(el=>parseFloat(getComputedStyle(el).fontSize)>=16);
   await input.fill('候选词');await input.evaluate(el=>el.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',keyCode:229,bubbles:true,cancelable:true})));await page.waitForTimeout(100);checks.ime229_no_send=await input.inputValue()==='候选词';
   await page.getByRole('button',{name:'打开导航',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'设置',exact:true}).click();await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();await page.getByRole('textbox',{name:'名称',exact:true}).waitFor();checks.narrow_settings=true;
   await page.screenshot({path:path.join(out,'mobile-'+name+'.png')});checks.no_runtime_errors=errors.length===0;assert(Object.values(checks).every(Boolean));
  }finally{results.push({name,checks,errors});fs.writeFileSync(path.join(out,'browsers.json'),JSON.stringify(results,null,2));await browser.close();}
 }
 console.log(JSON.stringify({browsers:results}));
})().catch(error=>{console.error(error.message);process.exitCode=1;});
