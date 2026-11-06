// Real browser + restricted bridge doubles. Native WebView2 is a separate check.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!out)throw new Error('Use the isolated frontend acceptance runner.');
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')),key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}}),checks={},errors=[];
 page.on('pageerror',error=>errors.push(error.message));
 await page.addInitScript(()=>{window.__external=[];window.faOpenExternal=async url=>{window.__external.push(url);};window.faSetBusy=async()=>{};window.faSetTheme=async theme=>{window.__nativeTheme=theme;};});
 try{
  await page.context().grantPermissions(['clipboard-read','clipboard-write']);await page.goto(info.origin+'/#key='+key);const input=page.getByRole('textbox',{name:'消息',exact:true});await input.waitFor();await input.fill('剪贴板测试');await input.evaluate(el=>{el.focus();el.setSelectionRange(0,el.value.length);});
  await input.click({button:'right'});const menu=page.getByRole('menu',{name:'编辑菜单',exact:true});await menu.getByRole('menuitem',{name:'复制',exact:true}).click();checks.copy=await page.evaluate(()=>navigator.clipboard.readText())==='剪贴板测试';
  await page.evaluate(()=>navigator.clipboard.writeText('粘贴后的受控输入'));await input.fill('');await input.click({button:'right'});await menu.getByRole('menuitem',{name:'粘贴',exact:true}).click();checks.controlled_paste=await input.inputValue()==='粘贴后的受控输入';
  await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: 粘贴后的受控输入',{exact:true}).waitFor();checks.paste_reaches_core=true;
  await page.evaluate(()=>{const link=document.createElement('a');link.id='bridge-external';link.href='https://example.com/freeagent-test';link.textContent='外链测试';document.body.append(link);});await page.locator('#bridge-external').click();checks.external_uses_bridge=(await page.evaluate(()=>window.__external)).includes('https://example.com/freeagent-test')&&new URL(page.url()).origin===info.origin;
  await page.locator('#bridge-external').click({button:'middle'});checks.middle_link_uses_bridge=await page.evaluate(()=>window.__external.length)===2;
  checks.theme_sync=await page.evaluate(()=>['light','dark','system'].includes(window.__nativeTheme));checks.no_runtime_errors=errors.length===0;
  fs.writeFileSync(path.join(out,'bridge.json'),JSON.stringify({checks,errors,native_webview:false},null,2));console.log(JSON.stringify({checks,errors,native_webview:false}));assert(Object.values(checks).every(Boolean));
 }finally{await browser.close();}
})().catch(error=>{console.error(error.message);process.exitCode=1;});
