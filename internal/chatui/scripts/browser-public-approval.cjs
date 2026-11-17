// Actual isolated HTTPS PublicServer, saved search policy and file approval.
const {chromium} = require('playwright');
const assert = require('node:assert/strict'), fs = require('node:fs'), path = require('node:path');
(async()=>{
 const origin=process.env.FREEAGENT_PUBLIC_SEARCH_ORIGIN,out=process.env.FREEAGENT_PUBLIC_SEARCH_ARTIFACTS;
 assert(origin&&out);fs.mkdirSync(out,{recursive:true});
 const browser=await chromium.launch({headless:true,channel:process.env.FREEAGENT_BROWSER_CHANNEL||'chrome'});
 const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1440,height:960}});
 const page=await context.newPage(),errors=[],checks={};page.on('pageerror',error=>errors.push(error.message));
 try{
  await page.goto(origin+'/?chat='+process.env.FREEAGENT_PUBLIC_APPROVAL_CHAT);
  await page.getByLabel('管理员密码',{exact:true}).fill('public fixture only password');
  await page.getByRole('button',{name:'进入工作台',exact:true}).click();
  const input=page.getByRole('textbox',{name:'消息',exact:true});await input.waitFor();
  await page.route('**/api/chats/*/preview?*',route=>route.fulfill({status:409,json:{code:'config',error:'执行配置已变化。'}}),{times:1});
  await input.fill('创建审批测试文件');await page.getByRole('button',{name:'发送消息',exact:true}).click();
  const card=page.getByRole('region',{name:'待确认操作',exact:true});
  await card.getByText('预览未就绪',{exact:true}).waitFor();
  assert(await card.getByRole('button',{name:'批准并继续',exact:true}).isDisabled());
  assert.equal(await card.getByText('正在读取精确变更…',{exact:true}).count(),0);
  await input.fill('等待确认期间保留草稿');await input.focus();
  assert(await input.evaluate(e=>e===document.activeElement));
  for(const [width,height] of [[1440,960],[390,844],[320,568]]){
   await page.setViewportSize({width,height});
   await card.getByRole('button',{name:'重新读取预览',exact:true}).scrollIntoViewIfNeeded();
   assert(await card.evaluate(e=>e.scrollWidth<=e.clientWidth+1),'error card overflow '+width);
   await page.screenshot({path:path.join(out,'approval-retry-'+width+'.png')});
  }
  checks.failed_preview_clear_state_retry_and_disabled_approval=true;
  await card.getByRole('button',{name:'重新读取预览',exact:true}).click();
  await card.locator('.approval-preview').filter({hasText:'approval.txt'}).waitFor();
  assert(await card.getByRole('button',{name:'批准并继续',exact:true}).isEnabled());
  assert.equal(await input.inputValue(),'等待确认期间保留草稿');
  checks.frozen_search_preview_retry_preserves_draft=true;
  await page.reload();
  await card.locator('.approval-preview').filter({hasText:'approval.txt'}).waitFor();
  assert(await card.getByRole('button',{name:'批准并继续',exact:true}).isEnabled());
  for(const [width,height] of [[1440,960],[1024,768],[768,1024],[390,844],[320,568]]){
   await page.setViewportSize({width,height});
   await card.scrollIntoViewIfNeeded();
   assert(await card.evaluate(e=>e.scrollWidth<=e.clientWidth+1),'preview card overflow '+width);
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
   await page.screenshot({path:path.join(out,'approval-ready-'+width+'.png')});
  }
  checks.reload_and_five_widths=true;
  await card.getByRole('button',{name:'批准并继续',exact:true}).click();
  await page.getByText('Approval completed exactly once.',{exact:true}).waitFor();
  assert.equal(await card.count(),0);
  checks.explicit_confirmation_completed=true;
  await page.route('**/api/chats/*/preview?*',route=>route.fulfill({status:409,json:{code:'config',error:'执行配置已变化。'}}));
  await input.fill('测试结束无法预览的待确认任务');
  await page.getByRole('button',{name:'发送消息',exact:true}).click();
  await card.getByRole('button',{name:'结束任务',exact:true}).waitFor();
  page.once('dialog',dialog=>dialog.accept());
  await card.getByRole('button',{name:'结束任务',exact:true}).click();
  await card.waitFor({state:'hidden'});
  checks.failed_preview_can_explicitly_end_without_tool_dispatch=true;
  assert.deepEqual(errors,[]);
  fs.writeFileSync(path.join(out,'public-approval-results.json'),JSON.stringify({checks,errors},null,2));
  console.log(JSON.stringify({checks,errors}));
 }catch(error){await page.screenshot({path:path.join(out,'approval-failure.png')});throw error;}
 finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
