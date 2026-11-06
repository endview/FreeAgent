const fs=require('node:fs'),path=require('node:path');
const {chromium}=require('playwright');
const artifacts=process.env.FREEAGENT_UI_ARTIFACTS;
if(!artifacts||!process.env.FREEAGENT_UI_TEST_ROOT)throw new Error('Use scripts/test-frontend.py with an isolated Core.');
fs.mkdirSync(artifacts,{recursive:true});
(async()=>{
 const root=path.join(process.env.FREEAGENT_UI_TEST_ROOT,'data'),info=JSON.parse(fs.readFileSync(root+'/engine.json','utf8')),key=fs.readFileSync(root+'/chat.secret','utf8').trim();
 const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true}),page=await browser.newPage({viewport:{width:1440,height:1024}});
 const errors=[],failures=[],checks={};page.on('pageerror',e=>errors.push(e.message));page.on('response',r=>{if(r.url().includes('/api/')&&r.status()>=400)failures.push({path:new URL(r.url()).pathname,status:r.status()});});
 page.on('dialog',dialog=>dialog.accept());
 try{
  await page.goto(info.origin+'/?view=settings&section=general#key='+key);
  const names=['通用','空间与会话','模型与 Key','权限与执行','文件范围','命令执行','后台命令','上下文','内置工具','MCP','Skill','Memory 与固定内容','扩展','渠道','公网安全','数据库与备份','附件','诊断','高级策略'];
  for(const name of names){await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name,exact:true}).click();await page.locator('.settings-heading h1').filter({hasText:name}).waitFor();await page.waitForTimeout(250);}
  checks.all_pages_open=errors.length===0&&failures.length===0;
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'上下文',exact:true}).click();
  await page.getByRole('spinbutton',{name:'默认输出 token 上限',exact:true}).fill('4097');await page.getByRole('button',{name:'保存设置',exact:true}).click();
  await page.getByRole('status').filter({hasText:'设置已保存'}).waitFor();checks.context_save=true;
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'内置工具',exact:true}).click();
  await page.getByRole('checkbox',{name:'网络工具',exact:true}).check();await page.getByRole('textbox',{name:'允许的网络来源（每行一个）',exact:true}).fill('https://example.com');await page.getByRole('button',{name:'保存设置',exact:true}).click();
  await page.getByRole('status').filter({hasText:'设置已保存'}).waitFor();checks.enable_network=true;
  await page.getByRole('checkbox',{name:'网络工具',exact:true}).uncheck();await page.getByRole('button',{name:'保存设置',exact:true}).click();await page.getByRole('status').filter({hasText:'设置已保存'}).waitFor();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'公网安全',exact:true}).click();
  await page.getByRole('checkbox',{name:'登录 Cookie 回传检测',exact:true}).waitFor();checks.cookie_policy_visible=true;
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'高级策略',exact:true}).click();
  await page.getByText('自动模型路由',{exact:true}).click();await page.getByRole('button',{name:'预览并保存',exact:true}).first().click();await page.getByRole('region',{name:'待确认操作',exact:true}).waitFor();await page.getByRole('button',{name:'拒绝',exact:true}).click();await page.getByRole('region',{name:'待确认操作',exact:true}).waitFor({state:'hidden'});checks.control_preview_reject=true;
  await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'返回设置列表',exact:true}).click();
  await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();await page.locator('.settings-heading h1').getByText('模型与 Key',{exact:true}).waitFor();checks.mobile_secondary_navigation=true;
  checks.no_runtime_errors=errors.length===0;checks.no_api_failures=failures.length===0;
  await page.screenshot({path:path.join(artifacts,'freeagent-v81-settings.png')});console.log(JSON.stringify({checks,errors,failures}));fs.writeFileSync(path.join(artifacts,'v81-settings-results.json'),JSON.stringify({checks,errors,failures},null,2));
  if(!Object.values(checks).every(Boolean))throw new Error('settings checks failed');
 }catch(error){console.log(JSON.stringify({checks,errors,failures}));await page.screenshot({path:path.join(artifacts,'freeagent-v81-settings-failure.png')});throw error;}
 finally{await browser.close();}
})().catch(e=>{console.error(e.message);process.exitCode=1;});
