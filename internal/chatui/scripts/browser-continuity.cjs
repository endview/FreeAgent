const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS;
 if(!root||!out)throw new Error('Use the isolated frontend acceptance runner.');
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8')),key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}),page=await browser.newPage({viewport:{width:1440,height:1000}});
 const checks={},errors=[];page.on('pageerror',error=>errors.push(error.message));
 const tag=Date.now().toString(),prompt='分支原文_'+tag;
 let rename='重命名_'+tag;page.on('dialog',dialog=>dialog.accept(dialog.type()==='prompt'?rename:undefined));
 try{
  await page.goto(info.origin+'/#key='+key);const input=page.getByRole('textbox',{name:'消息',exact:true});await input.fill(prompt);await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: '+prompt,{exact:true}).waitFor();
  await page.getByRole('button',{name:'重命名会话',exact:true}).click();await page.locator('.breadcrumb strong').getByText(rename,{exact:true}).waitFor();checks.rename=true;
  const oldChat=new URL(page.url()).searchParams.get('chat');rename='分支下一轮_'+tag;
  await page.locator('.user-message').last().getByRole('button',{name:'从这里重新开始',exact:true}).click();await page.getByText('Echo: '+rename,{exact:true}).waitFor();checks.branch_new_conversation=new URL(page.url()).searchParams.get('chat')!==oldChat;
  await input.fill('/compact');await page.getByRole('region',{name:'可用命令',exact:true}).waitFor();await page.getByRole('region',{name:'可用命令',exact:true}).getByRole('button').first().click();await page.getByRole('dialog').getByRole('heading',{name:'上下文管理',exact:true}).waitFor();checks.slash_context=true;await page.keyboard.press('Escape');
  await page.getByRole('textbox',{name:'搜索对话',exact:true}).fill('重命名_'+tag);await page.getByRole('list',{name:'会话列表',exact:true}).getByRole('button',{name:'重命名_'+tag,exact:true}).waitFor();checks.search=true;
  await page.getByRole('list',{name:'会话列表',exact:true}).getByRole('button',{name:'重命名_'+tag,exact:true}).click();await page.getByText('Echo: '+prompt,{exact:true}).waitFor();checks.original_history_kept=true;
  await page.reload();await page.getByText('Echo: '+prompt,{exact:true}).waitFor();checks.refresh=true;
  const second=await browser.newPage();await second.goto(info.origin+'/?chat='+oldChat+'#key='+key);await second.getByText('Echo: '+prompt,{exact:true}).waitFor();await second.close();
  await input.fill('其他窗口关闭后_'+tag);await page.getByRole('button',{name:'发送消息',exact:true}).click();await page.getByText('Echo: 其他窗口关闭后_'+tag,{exact:true}).waitFor();checks.other_client_close_keeps_owner=true;
  checks.no_runtime_errors=errors.length===0;
  fs.writeFileSync(path.join(out,'continuity.json'),JSON.stringify({checks,errors},null,2));console.log(JSON.stringify({checks,errors}));assert(Object.values(checks).every(Boolean));
 }catch(error){await page.screenshot({path:path.join(out,"continuity-failure.png")});console.log(JSON.stringify({checks,errors}));throw error;}finally{await browser.close();}
})().catch(error=>{console.error(error.message);process.exitCode=1;});
