// Real production assets + isolated Core; no real Provider or channel traffic.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const { chromium } = require('playwright');
const viewports = [[2560,1080],[1920,1080],[1440,900],[1366,768],[1280,720],[1024,768],[768,1024],[430,932],[390,844],[375,667],[320,568],[844,390]];
(async () => {
  const root = process.env.FREEAGENT_UI_TEST_ROOT, out = process.env.FREEAGENT_UI_ARTIFACTS;
  if (!root || !out) throw Error('Use the isolated frontend runner.');
  fs.mkdirSync(out, {recursive:true});
  const info = JSON.parse(fs.readFileSync(path.join(root,'data/engine.json'),'utf8'));
  const key = fs.readFileSync(path.join(root,'data/chat.secret'),'utf8').trim();
  const headers = {'X-FreeAgent-Key':key,Origin:info.origin,'Content-Type':'application/json'};
  async function api(route, body) {
    const r = await fetch(info.origin+'/api/'+route,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body)});
    const v = await r.json(); assert(r.ok,route+': '+(v.error||r.status)); return v;
  }
  const browser = await chromium.launch({...(process.env.FREEAGENT_BROWSER_CHANNEL?{channel:process.env.FREEAGENT_BROWSER_CHANNEL}:{}),headless:true});
  const page = await browser.newPage({viewport:{width:1440,height:900}}), results=[], errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  const bounded = async (locator,label) => {
    await locator.waitFor({state:'visible'});
    const r=await locator.boundingBox(),v=page.viewportSize();
    assert(r.x>=-1&&r.y>=-1&&r.x+r.width<=v.width+1&&r.y+r.height<=v.height+1,label+' outside viewport '+JSON.stringify({r,v}));
    return r;
  };
  async function settings(section,label,width,chat) {
    await page.goto(info.origin+'/?view=settings&section='+section+'&chat='+chat);
    if (width<=760) await page.getByRole('navigation',{name:'设置分类',exact:true}).getByRole('button',{name:label,exact:true}).click();
    await page.locator('.settings-heading h1').filter({hasText:label}).waitFor();
  }
  try {
    const chat = await api('chats',{title:'布局检查 · 长中文标题与跨窗口接续'});
    const prompt='合成布局资料：'+ '保留清楚的状态、可见的保存入口和完整的原始记录。'.repeat(10)+'\n\nhttps://example.invalid/'+ 'long-reference-'.repeat(16)+'\n\n```text\n'+ 'a'.repeat(180)+'\n```';
    await page.goto(info.origin+'/?chat='+chat.id+'#key='+key);
    await page.getByRole('textbox',{name:'消息',exact:true}).fill(prompt);
    await page.getByRole('button',{name:'发送消息',exact:true}).click();
    await page.locator('.assistant-message').filter({hasText:'Echo: 合成布局资料'}).last().waitFor();
    for (let i=0;i<viewports.length;i++) {
      const [width,height]=viewports[i],theme=i%2?'dark':'light';
      await page.setViewportSize({width,height});
      await page.emulateMedia({colorScheme:theme,reducedMotion:i%2?'reduce':'no-preference'});
      await page.goto(info.origin+'/?chat='+chat.id);
      await bounded(page.getByRole('textbox',{name:'消息',exact:true}),'composer');
      await bounded(page.getByRole('button',{name:'发送消息',exact:true}),'send button');
      assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'chat overflow');
      await page.screenshot({path:path.join(out,`layout-chat-${width}x${height}-${theme}.png`)});
      await page.getByRole('button',{name:'本轮能力',exact:true}).click();
      const popover=await bounded(page.locator('.capability-popover'),'capabilities');
      await page.keyboard.press('Escape');
      await page.getByRole('button',{name:'上下文',exact:true}).click();
      const dialog=page.getByRole('dialog',{name:'上下文管理',exact:true});
      const context=await bounded(dialog,'context dialog');
      assert(await dialog.evaluate(e=>e.scrollWidth<=e.clientWidth+1),'context dialog content overflow');
      await page.screenshot({path:path.join(out,`layout-context-dialog-${width}x${height}-${theme}.png`)});
      await page.keyboard.press('Escape');
      if(width<=1100) {
        await page.getByRole('button',{name:'打开导航',exact:true}).click();
        await bounded(page.getByRole('dialog',{name:'会话导航',exact:true}),'conversation navigation');
        await page.keyboard.press('Escape');
      }
      for(const [section,label] of [['context','上下文'],['providers','模型与 Key'],['extensions','扩展']]) {
        await settings(section,label,width,chat.id);
        const detail=page.locator('.settings-detail');
        assert(await detail.evaluate(e=>e.scrollWidth<=e.clientWidth+1),'settings horizontal overflow '+section);
        await bounded(page.getByRole('button',{name:'返回对话',exact:true}),'back to chat');
        await page.screenshot({path:path.join(out,`layout-${section}-${width}x${height}-${theme}.png`)});
        if(section==='context') {
          const lastField=page.getByRole('spinbutton',{name:'模型请求自动恢复次数',exact:true});
          await lastField.scrollIntoViewIfNeeded();await lastField.focus();
          assert(await lastField.evaluate(e=>{const r=e.getBoundingClientRect();return document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)===e;}),'save row covers the final setting');
          const save=page.getByRole('button',{name:'保存设置',exact:true});
          await save.scrollIntoViewIfNeeded();await bounded(save,'context save');
        }
      }
      results.push({width,height,theme,chat:true,settings:true,capabilities:popover,context_dialog:context});
    }
    await page.setViewportSize({width:1440,height:900});
    await settings('general','通用',1440,chat.id);
    assert.equal(await page.locator('.scope-label').innerText(),'当前客户端');
    const search=page.getByRole('textbox',{name:'查找设置',exact:true});await search.fill('上下文');
    const nav=page.getByRole('navigation',{name:'设置分类'});assert.equal(await nav.getByRole('button').count(),1);
    await nav.getByRole('button',{name:'上下文',exact:true}).click();assert.equal(await page.locator('.scope-label').innerText(),'Core 默认设置');
    await search.fill('');
    const summary={viewports:results,errors,settings_search:true,scope_labels:true,real_device:false};
    fs.writeFileSync(path.join(out,'layout-results.json'),JSON.stringify(summary,null,2));
    console.log(JSON.stringify({viewports:results.length,errors,all_geometry_checks_passed:true,settings_search:true,scope_labels:true}));
    assert.equal(errors.length,0);
  } catch(error) {
    await page.screenshot({path:path.join(out,'layout-failure.png')});
    fs.writeFileSync(path.join(out,'layout-partial.json'),JSON.stringify({results,errors},null,2));
    throw error;
  } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
