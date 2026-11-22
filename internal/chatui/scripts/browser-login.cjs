// Production assets/Core with synthetic HTTPS authentication responses.
// The Go public-web tests separately verify actual cookies, lockout and CSRF.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const { chromium, firefox, webkit } = require('playwright');
(async () => {
  const root = process.env.FREEAGENT_UI_TEST_ROOT, out = process.env.FREEAGENT_UI_ARTIFACTS;
  assert(root && out); fs.mkdirSync(out, { recursive: true });
  const info = JSON.parse(fs.readFileSync(path.join(root, 'data/engine.json'), 'utf8'));
  const key = fs.readFileSync(path.join(root, 'data/chat.secret'), 'utf8').trim();
  const checks = {}, layouts = [], errors = [];
  for (const [name, engine] of Object.entries({ chromium, firefox, webkit })) {
    const browser = await engine.launch({ headless: true, ...(name === 'chromium' ? { channel: process.env.FREEAGENT_BROWSER_CHANNEL || 'chrome' } : {}) });
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: 'light' });
    const page = await context.newPage();
    page.on('pageerror', error => errors.push({ name, message: error.message }));
    await page.addInitScript(() => {
      window.__cspViolations = [];
      document.addEventListener('securitypolicyviolation', e => window.__cspViolations.push(e.violatedDirective));
    });
    let authenticated = false, loginCount = 0, sessionCount = 0, earlyReads = 0, resolvePending, submitted;
    let sessionFailure = false;
    await context.route('https://**/*', async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.pathname === '/api/public/session') {
        sessionCount++;
        return route.fulfill({ status: sessionFailure ? 403 : 200, json: sessionFailure ? { error: '访问受限，请联系管理员。', code: 'access_denied' } : { public: true, authenticated, csrf: authenticated ? 'synthetic-csrf' : '', cookie_check: true } });
      }
      if (url.pathname === '/api/public/login') {
        loginCount++; submitted = request.postDataJSON();
        const result = await new Promise(resolve => { resolvePending = resolve; });
        if (result.authenticated) authenticated = true;
        return route.fulfill({ status: result.authenticated ? 200 : 401, json: result });
      }
      if (url.pathname.startsWith('/api/') && !authenticated) {
        earlyReads++; return route.fulfill({ status: 401, json: { error: 'not authenticated' } });
      }
      if (url.pathname === '/api/events') return route.fulfill({ contentType: 'text/event-stream', body: ': synthetic login-test connection\n\n' });
      const pathname = url.pathname.replace(/^\/api\/remote\//, '/api/attach/');
      const response = await fetch(info.origin + pathname + url.search, {
        method: request.method(), headers: { Origin: info.origin, 'X-FreeAgent-Key': key, ...(request.postData() ? { 'Content-Type': 'application/json' } : {}) },
        body: request.postData() || undefined,
      });
      const headers = Object.fromEntries(response.headers);
      delete headers['content-encoding']; delete headers['content-length']; delete headers['transfer-encoding'];
      return route.fulfill({ status: response.status, headers, body: Buffer.from(await response.arrayBuffer()) });
    });
    try {
      await page.goto('https://login.example.test:9443/?view=settings&section=providers');
      const input = page.getByLabel('管理员密码', { exact: true });
      await input.waitFor();
      assert.equal(await page.locator('.login-host').innerText(), 'login.example.test:9443');
      assert.equal(await input.getAttribute('type'), 'password');
      await page.getByRole('button', { name: '进入工作台', exact: true }).click();
      assert.equal(loginCount, 0);
      await input.fill('synthetic-password-only');
      await page.getByRole('button', { name: '显示密码' }).click();
      assert.equal(await input.getAttribute('type'), 'text');
      await page.getByRole('button', { name: '隐藏密码' }).click();
      assert.equal(await input.getAttribute('type'), 'password');
      for (const message of ['登录失败，请检查密码。', 'Cookie 检测未通过，请允许此站点使用 Cookie。']) {
        await input.fill('synthetic-password-only');
        const before = loginCount, previousSessions = sessionCount;
        await page.getByRole('button', { name: '进入工作台', exact: true }).click();
        await page.getByRole('button', { name: '正在登录…', exact: true }).waitFor();
        await page.waitForFunction(() => document.querySelector('form').getAttribute('aria-busy') === 'true');
        await page.locator('form').evaluate(form => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
        // Wait on the actual intercepted request, rather than a fixed UI delay.
        await expectEventually(() => loginCount === before + 1 && resolvePending);
        assert.equal(sessionCount, previousSessions + 1);
        assert.equal(submitted.password, 'synthetic-password-only');
        assert.equal(submitted.username, 'admin');
        assert.equal(await input.inputValue(), '');
        assert(await input.evaluate(e => e.readOnly));
        resolvePending({ error: message, code: 'login_failed' }); resolvePending = undefined;
        await page.getByRole('alert').filter({ hasText: message }).waitFor();
        assert.equal(loginCount, before + 1);
        assert.equal(await input.getAttribute('type'), 'password');
      }
      checks[name + '_login_states_and_single_submission'] = true;
      for (const [width, height, colorScheme] of name === 'chromium' ? [[2560,1080,'light'],[1440,900,'light'],[1024,768,'dark'],[768,1024,'light'],[390,844,'light'],[320,568,'dark'],[844,390,'light']] : [[390,844,'light']]) {
        await page.setViewportSize({ width, height }); await page.emulateMedia({ colorScheme });
        const measured = await page.evaluate(() => {
          const input = document.querySelector('#login-password'), button = document.querySelector('.login-enter');
          return { overflow: document.documentElement.scrollWidth > innerWidth + 1, inputFont: parseFloat(getComputedStyle(input).fontSize), inputHeight: input.getBoundingClientRect().height, buttonHeight: button.getBoundingClientRect().height, csp: window.__cspViolations };
        });
        assert(!measured.overflow); assert(measured.inputFont >= 16); assert(measured.inputHeight >= 44); assert(measured.buttonHeight >= 44); assert.deepEqual(measured.csp, []);
        layouts.push({ name, width, height, colorScheme, ...measured });
        if (name === 'chromium') await page.screenshot({ path: path.join(out, `login-${width}-${height}.png`), fullPage: true });
      }
      await page.getByText('无法登录？', { exact: true }).click();
      assert(await page.getByText('忘记密码，请联系此实例管理员。').isVisible());
      assert.equal(earlyReads, 0);
      assert(!JSON.stringify(await page.evaluate(() => ({ ...localStorage, ...sessionStorage }))).includes('synthetic-password-only'));
      sessionFailure = true;
      await page.goto('https://different-instance.example.test/');
      await page.getByRole('alert').filter({ hasText: '访问受限，请联系管理员。' }).waitFor();
      assert.equal(await page.locator('.login-host').innerText(), 'different-instance.example.test');
      sessionFailure = false;
      await page.goto('https://login.example.test:9443/?view=settings&section=providers');
      await input.fill('synthetic-password-only');
      await page.getByRole('button', { name: '进入工作台', exact: true }).click();
      await expectEventually(() => resolvePending);
      resolvePending({ public: true, authenticated: true, csrf: 'synthetic-csrf' }); resolvePending = undefined;
      await page.locator('.settings-heading h1').filter({ hasText: '模型与 Key' }).waitFor();
      assert.equal(new URL(page.url()).searchParams.get('section'), 'providers');
      await page.reload();
      await page.locator('.settings-heading h1').filter({ hasText: '模型与 Key' }).waitFor();
      checks[name + '_dynamic_host_auth_boundary_and_session_restore'] = true;
    } finally { await browser.close(); }
  }
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(out, 'login-results.json'), JSON.stringify({ checks, layouts, errors, auth: 'synthetic HTTPS contract; production assets and isolated real Core', no_live_credentials: true }, null, 2));
  console.log(JSON.stringify({ checks, layout_count: layouts.length, errors }));
})();
async function expectEventually(check) {
  const deadline = Date.now() + 10000;
  while (!check()) { if (Date.now() > deadline) throw new Error('Expected intercepted request'); await new Promise(resolve => setTimeout(resolve, 20)); }
}
