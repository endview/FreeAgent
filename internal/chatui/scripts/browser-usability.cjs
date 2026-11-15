// Interaction review against a disposable Core. No production credentials or traffic.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const { chromium } = require('playwright');
const sections = [['general','通用'],['providers','模型与 Key'],['context','上下文'],['permissions','权限与执行'],['commands','命令执行'],['tools','内置工具'],['extensions','扩展'],['memory','Memory 与固定内容'],['public','公网安全'],['data','数据库与备份']];
(async () => {
  const root=process.env.FREEAGENT_UI_TEST_ROOT, out=process.env.FREEAGENT_UI_ARTIFACTS;
  assert(root && out,'Use the isolated frontend runner'); fs.mkdirSync(out,{recursive:true});
  const info=JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8'));
  const key=fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
  const browser=await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}}), checks={}, errors=[], screens=[];
  const auditOnly=process.env.FREEAGENT_UI_AUDIT_ONLY==='1';
  page.on('pageerror',e=>errors.push(e.message));
  page.on('dialog',dialog=>dialog.accept());
  async function open(section,label,width=1440) {
    await page.goto(info.origin+'/?view=settings&section='+section);
    await page.locator('.settings-layout').waitFor();
    const heading=page.locator('.settings-heading h1');
    if(width<=760 && !(await heading.isVisible())) {
      if(section==='context') checks.mobile_deep_link_opens_detail=false;
      await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:label,exact:true}).click();
    } else if(width<=760 && section==='context') checks.mobile_deep_link_opens_detail=true;
    await heading.filter({hasText:label}).waitFor();
    await page.locator('.loading-label').waitFor({state:'hidden'});
  }
  try {
    await page.goto(info.origin+'/?view=settings&section=general#key='+key);
    for(const [width,height] of [[1440,900],[390,844]]) {
      await page.setViewportSize({width,height});
      for(const [section,label] of sections) {
        await open(section,label,width);
        const overflow=await page.locator('.settings-detail').evaluate(e=>e.scrollWidth>e.clientWidth+1);
        const missingNames=await page.locator('.settings-detail').evaluate(root=>[...root.querySelectorAll('input,textarea,select,button')].filter(e=>e.getClientRects().length&&!e.hidden&&!e.textContent.trim()&&!e.getAttribute('aria-label')&&!e.getAttribute('aria-labelledby')&&!(e.labels?.length)).map(e=>({tag:e.tagName,type:e.type,placeholder:e.getAttribute('placeholder')})));
        screens.push({section,width,height,overflow,missingNames});
        await page.screenshot({path:path.join(out,`usability-${section}-${width}.png`)});
      }
    }
    await page.setViewportSize({width:1440,height:900});
    await open('general','通用');
    const sidebar=page.getByRole('spinbutton',{name:'侧栏宽度（px）',exact:true});
    await sidebar.focus();await sidebar.press('ControlOrMeta+A');await sidebar.press('Backspace');await sidebar.pressSequentially('300');await sidebar.press('Tab');
    checks.layout_number_can_be_retyped=await sidebar.inputValue()==='300';
    if(!auditOnly) {
      await sidebar.fill('12');await sidebar.press('Tab');
      checks.invalid_dimension_has_field_error=await sidebar.getAttribute('aria-invalid')==='true';
      await sidebar.focus();await sidebar.press('Escape');await sidebar.press('Tab');
      checks.invalid_dimension_preserves_saved_value=await sidebar.inputValue()==='300' && await sidebar.getAttribute('aria-invalid')!=='true';
    }
    await open('providers','模型与 Key');
    await page.getByRole('textbox',{name:'名称',exact:true}).fill('待保存的本地配置');
    checks.provider_draft_has_status=await page.getByText('有未保存的修改',{exact:true}).count()>0;
    checks.secret_hint_is_associated=await page.getByLabel('API Key',{exact:false}).getAttribute('aria-describedby')!==null;
    checks.unsaved_reload_is_guarded=await page.evaluate(()=>!window.dispatchEvent(new Event('beforeunload',{cancelable:true})));
    await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'上下文',exact:true}).click();
    await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'模型与 Key',exact:true}).click();
    checks.provider_draft_survives_navigation=await page.getByRole('textbox',{name:'名称',exact:true}).inputValue()==='待保存的本地配置';
    if(!auditOnly) {
      const providerName=page.getByRole('textbox',{name:'名称',exact:true});
      await providerName.fill('');
      checks.reverted_draft_stops_warning=await page.evaluate(()=>window.dispatchEvent(new Event('beforeunload',{cancelable:true})));
      await page.getByRole('button',{name:'保存配置',exact:true}).click();
      checks.required_provider_field_is_identified=await providerName.getAttribute('aria-invalid')==='true';
      await open('context','上下文');
      const nav=page.getByRole('navigation',{name:'设置分类',exact:true});
      const search=page.getByRole('textbox',{name:'查找设置',exact:true});
      await search.fill('压缩');
      checks.setting_search_finds_purpose=await nav.getByRole('button',{name:'上下文',exact:true}).count()===1;
      await nav.getByRole('button',{name:'清除设置搜索',exact:true}).click();
      const output=page.getByRole('spinbutton',{name:'默认输出 token 上限',exact:true});
      await output.fill('6001');
      const saving=page.waitForResponse(r=>r.url().endsWith('/api/settings/context') && r.request().method()==='PATCH');
      await page.getByRole('button',{name:'保存设置',exact:true}).click();assert.equal((await saving).status(),200);
      await page.getByRole('status').filter({hasText:'设置已保存'}).waitFor();
      checks.success_clears_dirty_and_warning=await page.locator('.draft-status').innerText().then(x=>x.includes('与已保存设置一致')) && await page.evaluate(()=>window.dispatchEvent(new Event('beforeunload',{cancelable:true})));
      await output.fill('6002');
      checks.edit_hides_stale_success=await page.locator('.form-result').count()===0;
      await page.route('**/api/settings/context',route=>route.fulfill({status:409,contentType:'application/json',body:JSON.stringify({error:'配置已变化，请读取最新设置后重试。'})}));
      await page.getByRole('button',{name:'保存设置',exact:true}).click();
      await page.getByRole('alert').filter({hasText:'配置已变化'}).waitFor();
      checks.conflict_preserves_value_and_dirty=await output.inputValue()==='6002' && (await page.locator('.draft-status').innerText()).includes('有未保存的修改');
      await page.unroute('**/api/settings/context');
      await page.getByRole('button',{name:'读取最新并放弃草稿',exact:true}).click();
      await page.waitForFunction(()=>document.querySelector('.draft-status')?.textContent.includes('与已保存设置一致'));
      checks.discard_reads_saved_state=await output.inputValue()==='6001';
      await page.getByRole('combobox',{name:'摘要目标',exact:true}).click();
      await page.getByRole('option',{name:'手动上限',exact:true}).click();
      checks.manual_target_exposes_editable_limit=await page.getByRole('spinbutton',{name:'摘要上限（UTF-8 字节）',exact:true}).isEnabled();
      await page.getByRole('combobox',{name:'摘要目标',exact:true}).click();await page.getByRole('option',{name:'自动目标',exact:true}).click();
      checks.auto_target_does_not_claim_manual_number=await page.getByRole('spinbutton',{name:'摘要上限（UTF-8 字节）',exact:true}).count()===0;
      await page.getByRole('button',{name:'保存设置',exact:true}).click();await page.getByRole('status').filter({hasText:'设置已保存'}).waitFor();
      await page.setViewportSize({width:390,height:844});await open('context','上下文',390);
      await page.getByRole('button',{name:'返回设置列表',exact:true}).click();
      checks.mobile_back_url_matches_list=!(new URL(page.url())).searchParams.has('section') && await page.getByRole('navigation',{name:'设置分类',exact:true}).isVisible();
      await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:'通用',exact:true}).click();
      checks.mobile_navigation_moves_focus=await page.locator('.settings-heading h1').evaluate(e=>e===document.activeElement);
      await page.goBack();checks.browser_back_restores_list=await page.getByRole('navigation',{name:'设置分类',exact:true}).isVisible();
      await page.setViewportSize({width:320,height:568});await open('providers','模型与 Key',320);
      await page.getByRole('combobox',{name:'Provider 预设',exact:true}).click();
      const menu=page.getByRole('listbox');const rect=await menu.boundingBox();
      checks.select_menu_fits_small_viewport=rect.x>=0&&rect.y>=0&&rect.x+rect.width<=321&&rect.y+rect.height<=569;
      await page.keyboard.press('Escape');
      checks.select_escape_restores_focus=await page.getByRole('combobox',{name:'Provider 预设',exact:true}).evaluate(e=>e===document.activeElement);
    }
    checks.no_horizontal_overflow=screens.every(s=>!s.overflow);
    checks.no_unnamed_visible_controls=screens.every(s=>!s.missingNames.length);
    checks.no_runtime_errors=errors.length===0;
    fs.writeFileSync(path.join(out,'usability-results.json'),JSON.stringify({auditOnly,checks,screens,errors},null,2));
    console.log(JSON.stringify({auditOnly,checks,errors}));
    if(!auditOnly) assert(Object.values(checks).every(Boolean),'Usability checks failed; see usability-results.json');
  } catch(error) {await page.screenshot({path:path.join(out,'usability-failure.png')});throw error;}
  finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
