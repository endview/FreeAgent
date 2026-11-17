// Isolated HTTPS PublicServer, real login/session/CSRF and search fixture.
// Called by TestPublicSearchRealBrowser; no production credentials or traffic.
const { chromium } = require('playwright');
const { openSettingsPage } = require('./settings-navigation.cjs');
const assert = require('node:assert/strict');
const fs = require('node:fs'), path = require('node:path'), http = require('node:http');

(async () => {
  const origin = process.env.FREEAGENT_PUBLIC_SEARCH_ORIGIN;
  const output = process.env.FREEAGENT_PUBLIC_SEARCH_ARTIFACTS;
  assert(origin && output, 'Use the opt-in isolated Go browser test.');
  fs.mkdirSync(output, { recursive: true });
  let calls = 0;
  const search = http.createServer((req, res) => {
    calls++;
    const url = new URL(req.url, 'http://fixture');
    assert.equal(url.pathname, '/search');
    assert.equal(url.searchParams.get('q'), 'FreeAgent official documentation');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ results: [{ title: 'Test source', url: 'https://example.org/source', content: 'Synthetic result' }] }));
  });
  await new Promise(resolve => search.listen(0, '127.0.0.1', resolve));
  const browser = await chromium.launch({ headless: true, channel: process.env.FREEAGENT_BROWSER_CHANNEL || 'chrome' });
  // This is the test server's self-signed certificate, never a public site.
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 960 } });
  const page = await context.newPage(), errors = [], checks = {};
  page.on('pageerror', error => errors.push(error.message));
  async function choose(name, option) {
    await page.getByRole('combobox', { name, exact: true }).click();
    await page.getByRole('option', { name: option, exact: true }).click();
  }
  try {
    await page.goto(origin + '/?view=settings&section=search');
    await page.getByLabel('管理员密码', { exact: true }).fill('public fixture only password');
    await page.getByRole('button', { name: '进入工作台', exact: true }).click();
    await page.getByRole('button', { name: '保存搜索设置', exact: true }).waitFor();
    assert.equal(new URL(page.url()).searchParams.get('section'), 'search');
    checks.real_https_login_and_search_loaded = true;
    assert.equal(await page.getByRole('combobox', { name: '默认搜索连接', exact: true }).innerText(), '未选择');
    assert.equal(await page.getByRole('combobox', { name: '搜索 Key', exact: true }).innerText(), '选择已保存的 Key');

    await page.getByRole('button', { name: '设置网络工具', exact: true }).click();
    await page.getByRole('checkbox', { name: '网络工具', exact: true }).check();
    await page.getByRole('button', { name: '保存设置', exact: true }).click();
    await page.getByText('设置已保存。新任务使用新配置，已有任务保留自己的执行快照。', { exact: true }).waitFor();
    await openSettingsPage(page, '联网搜索');
    await page.getByRole('button', { name: '保存搜索设置', exact: true }).waitFor();
    assert.equal(await page.getByRole('button', { name: '设置网络工具', exact: true }).count(), 0);
    checks.network_settings_action_and_save = true;

    await choose('搜索服务', 'SearXNG（自托管）');
    await page.getByRole('textbox', { name: '连接名称', exact: true }).fill('日常搜索');
    await page.getByRole('button', { name: '新增搜索 Key', exact: true }).click();
    await page.getByRole('textbox', { name: 'Key 名称', exact: true }).fill('独立验收 Key');
    await page.getByRole('textbox', { name: '服务地址', exact: true }).fill('http://127.0.0.1:' + search.address().port);
    await page.getByRole('button', { name: '保存 Key', exact: true }).click();
    await page.getByText('Key 已保存。将它添加到连接后，再保存搜索设置。', { exact: true }).waitFor();
    await page.getByRole('button', { name: '添加到草稿', exact: true }).click();
    await choose('默认搜索方式', '指定搜索 API');
    await page.getByRole('button', { name: '保存搜索设置', exact: true }).click();
    await page.getByText('搜索设置已保存，用于后续任务。', { exact: true }).waitFor();
    assert.equal(calls, 0);
    await page.reload();
    await page.getByRole('button', { name: '测试连接', exact: true }).waitFor();
    assert.equal(await page.getByRole('combobox', { name: '默认搜索连接', exact: true }).innerText(), '日常搜索');
    await page.getByRole('button', { name: '测试连接', exact: true }).click();
    await page.getByText('连接正常，固定查询返回 1 条结果。测试不会自动更改搜索方式。', { exact: true }).waitFor();
    assert.equal(calls, 1);
    checks.create_key_connection_save_reload_probe = true;

    for (const [status, body] of [[502, '<html>upstream unavailable</html>'], [200, 'not json']]) {
      const broken = async route => route.fulfill({ status, contentType: 'text/html', body });
      await page.route('**/api/settings/web-search', broken);
      await page.reload();
      await page.getByRole('heading', { name: '搜索设置暂时无法加载', exact: true }).waitFor();
      assert.match(await page.getByRole('alert').innerText(), new RegExp('HTTP ' + status));
      assert.equal(await page.getByRole('button', { name: '保存搜索设置', exact: true }).count(), 0);
      if (status === 502) await page.screenshot({ path: path.join(output, 'search-retry-1440.png') });
      await page.unroute('**/api/settings/web-search', broken);
      await page.getByRole('button', { name: '重新加载搜索设置', exact: true }).click();
      await page.getByRole('button', { name: '保存搜索设置', exact: true }).waitFor();
    }
    assert.equal(calls, 1);
    checks.invalid_response_retry_without_empty_save_or_probe = true;

    for (const [width, height] of [[1440, 960], [1024, 768], [768, 1024], [390, 844], [320, 568]]) {
      await page.setViewportSize({ width, height });
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'page overflow at ' + width);
      const detail = page.locator('.settings-detail');
      assert(await detail.evaluate(element => element.scrollWidth <= element.clientWidth + 1), 'settings overflow at ' + width);
      const save = page.getByRole('button', { name: '保存搜索设置', exact: true });
      await save.scrollIntoViewIfNeeded();
      assert(await save.isVisible());
      await page.screenshot({ path: path.join(output, 'search-https-' + width + '.png') });
    }
    checks.five_widths_and_save_accessible = true;
    assert.deepEqual(errors, []);
    fs.writeFileSync(path.join(output, 'public-search-results.json'), JSON.stringify({ checks, errors, real_public_server: true }, null, 2));
    console.log(JSON.stringify({ checks, errors }));
  } catch (error) {
    await page.screenshot({ path: path.join(output, 'public-search-failure.png') });
    throw error;
  } finally {
    await browser.close();
    await new Promise(resolve => search.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
