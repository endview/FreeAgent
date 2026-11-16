// Exercise the approved settings hierarchy against an isolated real Core/PG.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
const {openSettingsPage}=require('./settings-navigation.cjs');
(async()=>{
 const root=process.env.FREEAGENT_UI_TEST_ROOT,out=process.env.FREEAGENT_UI_ARTIFACTS;
 assert(root&&out);fs.mkdirSync(out,{recursive:true});
 const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8'));
 const key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
 const headers={'Origin':info.origin,'X-FreeAgent-Key':key,'Content-Type':'application/json'};
 async function api(route,body){const r=await fetch(info.origin+'/api/'+route,{headers,method:body?'POST':'GET',body:body?JSON.stringify(body):undefined});const data=await r.json();assert(r.ok,JSON.stringify(data));return data;}
 const chat=await api('chats',{title:'设置导航合成验收'});
 const browser=await chromium.launch({channel:process.env.FREEAGENT_BROWSER_CHANNEL||'msedge',headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:960}}),checks={},errors=[];
 page.on('pageerror',e=>errors.push(e.message));
 try{
  await page.goto(info.origin+'/?chat='+chat.id+'#key='+key);
  await page.getByRole('button',{name:'设置',exact:true}).waitFor();
  assert.equal(await page.locator('.sidebar-footer > .nav-settings').count(),1);
  for(const name of ['管理空间内容','回收站','保留的执行记录'])assert.equal(await page.locator('.sidebar-footer').getByText(name,{exact:true}).count(),0);
  checks.workspace_only_settings_entry=true;
  const row=page.locator('[data-chat="'+chat.id+'"] .conversation-row'),more=row.locator('.conversation-more');
  await page.getByRole('button',{name:'新对话',exact:true}).focus();await page.mouse.move(900,600);
  assert.equal(await more.evaluate(e=>Number(getComputedStyle(e).opacity)),0);
  await row.hover();assert.equal(await more.evaluate(e=>Number(getComputedStyle(e).opacity)),1);
  await more.click();await page.getByRole('menuitem',{name:'选择多个会话…',exact:true}).click();
  await page.locator('.settings-heading h1').getByText('空间内容',{exact:true}).waitFor();
  await page.getByText('已选 1',{exact:true}).waitFor();
  assert.equal(new URL(page.url()).searchParams.get('view'),'settings');
  assert.equal(new URL(page.url()).searchParams.get('section'),'space-content');
  checks.menu_enters_settings_preserving_selection=true;
  const headings=page.locator('.settings-group-toggle');
  assert.deepEqual(await headings.locator('span').allTextContents(),['工作台设置','能力管理','连接与数据','空间管理']);
  assert.equal(await headings.locator('svg:first-child').count(),0);
  const ratio=await page.evaluate(()=>parseFloat(getComputedStyle(document.querySelector('.settings-group-toggle')).fontSize)/parseFloat(getComputedStyle(document.querySelector('.settings-nav-item')).fontSize));
  assert(Math.abs(ratio-1.3)<.001);checks.headings_and_ratio=true;
  assert.equal(await page.locator('.settings-nav-item').count(),23);
  assert.equal(await page.locator('.settings-nav-item > svg').count(),23);checks.all_setting_items_have_icons=true;
  const spaces=page.locator('.settings-nav-group').filter({has:page.getByRole('button',{name:'空间管理',exact:true})});
  assert.deepEqual(await spaces.locator('.settings-nav-item span').allTextContents(),['空间内容','回收站','保留的执行记录']);
  checks.space_group_three_pages=true;
  for(const name of ['工作台设置','能力管理','连接与数据','空间管理']){
   const toggle=page.getByRole('button',{name,exact:true});
   if(await toggle.getAttribute('aria-expanded')==='false')await toggle.click();
  }
  const navigationScroll=page.locator('.settings-nav-groups');
  await navigationScroll.evaluate(e=>e.scrollTop=e.scrollHeight);
  const navigationTop=await navigationScroll.evaluate(e=>e.scrollTop);
  assert(navigationTop>0,'expanded navigation must actually scroll');
  await openSettingsPage(page,'回收站');
  await page.locator('.settings-heading h1').getByText('回收站',{exact:true}).waitFor();
  assert(Math.abs(await navigationScroll.evaluate(e=>e.scrollTop)-navigationTop)<2,'selecting bottom item reset navigation scroll');
  await openSettingsPage(page,'保留的执行记录');
  await page.locator('.settings-heading h1').getByText('保留的执行记录',{exact:true}).waitFor();
  assert(Math.abs(await navigationScroll.evaluate(e=>e.scrollTop)-navigationTop)<2,'switching bottom pages reset navigation scroll');
  checks.expanded_navigation_keeps_scroll=true;
  await openSettingsPage(page,'空间内容');
  await page.getByRole('button',{name:'能力管理',exact:true}).scrollIntoViewIfNeeded();
  if(await page.getByRole('button',{name:'能力管理',exact:true}).getAttribute('aria-expanded')==='true')await page.getByRole('button',{name:'能力管理',exact:true}).click();
  await page.getByRole('button',{name:'能力管理',exact:true}).click();
  await openSettingsPage(page,'MCP');await page.locator('.settings-heading h1').getByText('MCP',{exact:true}).waitFor();
  await page.getByRole('button',{name:'能力管理',exact:true}).click();assert(!(await page.locator('.settings-nav-item').filter({hasText:/^MCP$/}).isVisible()));
  await page.getByRole('textbox',{name:'查找设置',exact:true}).fill('回收');
  assert(await page.locator('.settings-nav-item').filter({hasText:/^回收站$/}).isVisible());
  await page.getByRole('button',{name:'清除设置搜索',exact:true}).click();
  await openSettingsPage(page,'回收站');await page.locator('.settings-heading h1').getByText('回收站',{exact:true}).waitFor();
  await page.goBack();await page.locator('.settings-heading h1').getByText('MCP',{exact:true}).waitFor();
  await page.goForward();await page.locator('.settings-heading h1').getByText('回收站',{exact:true}).waitFor();
  checks.collapse_search_and_history=true;
  await openSettingsPage(page,'空间内容');await page.getByRole('button',{name:'空间设置',exact:true}).click();
  await page.getByRole('textbox',{name:'新空间名称',exact:true}).fill('导航空间');await page.getByRole('button',{name:'创建空间',exact:true}).click();
  await page.getByRole('heading',{name:'导航空间',exact:true}).waitFor();
  await page.getByRole('button',{name:'会话列表',exact:true}).click();
  await page.getByRole('button',{name:'附件与结果文件',exact:true}).click();await page.getByRole('button',{name:'返回会话列表',exact:true}).waitFor();
  await page.getByRole('button',{name:'返回会话列表',exact:true}).click();
  checks.space_form_and_attachment_manager_preserved=true;
  for(const [legacy,section] of [['manage','空间内容'],['trash','回收站'],['retained','保留的执行记录']]){
   await page.goto(info.origin+'/?view='+legacy+'#key='+key);
   await page.locator('.settings-heading h1').getByText(section,{exact:true}).waitFor();
   assert(await page.locator('.settings-nav').isVisible());
  }
  await page.goto(info.origin+'/?view=settings&section=spaces#key='+key);await page.getByRole('textbox',{name:'新空间名称',exact:true}).waitFor();
  checks.legacy_links_preserved=true;
  for(const [width,height] of [[1440,960],[1200,800],[1024,768],[768,1024],[390,844],[320,568],[844,390]]){
   await page.setViewportSize({width,height});
   await page.goto(info.origin+'/?view=settings&section=space-content#key='+key);await page.locator('.settings-heading h1').getByText('空间内容',{exact:true}).waitFor();
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
   const detail=page.locator('.settings-detail');assert(!(await detail.evaluate(e=>e.scrollWidth>e.clientWidth+1)),'settings overflow '+width);
   await page.screenshot({path:path.join(out,'settings-space-'+width+'x'+height+'.png')});
   if(width<=760){
    await page.getByRole('button',{name:'返回设置列表',exact:true}).click();assert(await page.getByRole('button',{name:'空间管理',exact:true}).isVisible());
    await page.screenshot({path:path.join(out,'settings-list-'+width+'x'+height+'.png')});
    await openSettingsPage(page,'保留的执行记录');await page.locator('.settings-heading h1').getByText('保留的执行记录',{exact:true}).waitFor();
   }
  }
  checks.seven_viewports_and_mobile_back=true;
  await page.setViewportSize({width:1440,height:960});
  await page.goto(info.origin+'/?view=settings&section=space-content#key='+key);
  await page.locator('.settings-group-toggle').first().waitFor();
  await page.getByRole('button',{name:'切换主题',exact:true}).click();
  if(!(await page.locator('html').evaluate(e=>e.classList.contains('dark'))))await page.getByRole('button',{name:'切换主题',exact:true}).click();
  await page.screenshot({path:path.join(out,'settings-dark.png')});
  await page.addStyleTag({content:'html{zoom:2}'});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));checks.dark_and_200_percent=true;
  checks.no_runtime_errors=errors.length===0;
  fs.writeFileSync(path.join(out,'settings-navigation-results.json'),JSON.stringify({checks,errors},null,2));
  assert(Object.values(checks).every(Boolean));console.log(JSON.stringify({checks,errors}));
 }catch(e){await page.screenshot({path:path.join(out,'settings-navigation-failure.png')});throw e;}finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
