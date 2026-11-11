// End-to-end interaction regressions against an isolated real Owner + PG.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const { chromium } = require('playwright');
const root = process.env.FREEAGENT_UI_TEST_ROOT, out = process.env.FREEAGENT_UI_ARTIFACTS;
if (!root || !out) throw Error('Run via scripts/test-frontend.py');
(async () => {
  const info = JSON.parse(fs.readFileSync(path.join(root, 'data/engine.json'), 'utf8'));
  const key = fs.readFileSync(path.join(root, 'data/chat.secret'), 'utf8').trim();
  const browser = await chromium.launch({ headless: true, ...(process.env.FREEAGENT_BROWSER_CHANNEL ? { channel: process.env.FREEAGENT_BROWSER_CHANNEL } : {}) });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1024 } });
  const errors = [], checks = {};
  page.on('pageerror', error => errors.push(error.message));
  page.on('dialog', dialog => dialog.type() === 'prompt' ? dialog.accept(dialog.defaultValue()) : dialog.accept());
  const api = async (route, body, method) => {
    const response = await fetch(info.origin + '/api/' + route, { method: method || (body === undefined ? 'GET' : 'POST'), headers: { 'X-FreeAgent-Key': key, Origin: info.origin, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) });
    const value = await response.json(); assert(response.ok, route + ': ' + JSON.stringify(value)); return value;
  };
  const input = () => page.getByRole('textbox', { name: '消息', exact: true });
  const send = () => page.getByRole('button', { name: '发送消息', exact: true });
  async function open(chat) {
    await page.goto(info.origin + '/?chat=' + chat.id + '#key=' + key);
    await page.getByRole('main').getByText(chat.title, { exact: true }).waitFor();
    await page.waitForFunction(() => !document.body.textContent.includes('正在读取目标会话；输入会保留。'));
  }
  async function seed(title, prompt) {
    const chat = await api('chats', { title }); await open(chat);
    await input().fill(prompt); await send().click();
    await page.getByText('Echo: ' + prompt, { exact: true }).waitFor();
    return { ...chat, run: (await api('chats/' + chat.id)).view.turns.at(-1).key };
  }
  async function plan(kind, args) {
    const op = await api('control/plan', { kind, arguments: args });
    const code = await api('control/confirmation?operation_id=' + op.operation_id);
    return api('control/confirm', { operation_id: op.operation_id, code: code.code });
  }
  try {
    const chat = await seed('Interaction source', 'source once');
    await page.getByRole('button', { name: '上下文', exact: true }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('摘要目标（UTF-8 字节）', { exact: true }).fill('8192');
    await dialog.getByRole('button', { name: '保存默认设置', exact: true }).click();
    await dialog.getByRole('status').filter({ hasText: '已保存为 8,192' }).waitFor();
    assert.equal((await api('settings')).context.task_summary_bytes, 8192);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: '上下文', exact: true }).click();
    assert.equal(await dialog.getByLabel('摘要目标（UTF-8 字节）', { exact: true }).inputValue(), '8192');
    checks.summary_save_read_reopen = true;
    await page.keyboard.press('Escape');

    await api('attach/inputs', { conversation_id: chat.id, operation: 'accept', command: { action: 'steer', run_id: chat.run, request_key: 'late', prompt: 'expired guidance' } });
    await page.locator('.queue-list').getByText('expired guidance', { exact: true }).waitFor();
    await page.getByRole('button', { name: '删除排队消息', exact: true }).click();
    await page.locator('.queue-list').getByText('expired guidance', { exact: true }).waitFor({ state: 'hidden' });
    assert.equal((await api('attach/inputs?conversation_id=' + chat.id)).entries[0].status, 'cancelled');
    checks.expired_guidance_cancel = true;
    await api('attach/inputs', { conversation_id: chat.id, operation: 'accept', command: { action: 'steer', run_id: chat.run, request_key: 'late2', prompt: 'old chat guidance' } });
    await page.locator('.queue-list').getByText('old chat guidance', { exact: true }).waitFor();
    await page.getByRole('button', { name: '新对话', exact: true }).click();
    assert.equal(await page.locator('.queue-list').getByText('old chat guidance', { exact: true }).count(), 0);
    checks.new_chat_has_no_old_queue = true;

    const branchSource = await seed('Branch fixture', 'branch once');
    const branchResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/api/attach/branch');
    await page.locator('.messages article').first().getByRole('button', { name: '从这里重新开始', exact: true }).click();
    const branch = await (await branchResponse).json();
    await page.getByText('Echo: branch once', { exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelector('textarea[aria-label="消息"]')?.value === '');
    assert.equal((await api('chats/' + branch.conversation.id)).view.turns.length, 1);
    assert(await send().isDisabled()); checks.branch_autosend_clears_draft = true;
    await open(branchSource);
    let firstBranch = '';
    await page.route(info.origin + '/api/attach/branch', async route => {
      const response = await route.fetch(); firstBranch = (await response.json()).conversation.id; await route.abort('connectionreset');
    }, { times: 1 });
    await page.locator('.messages article').first().getByRole('button', { name: '从这里重新开始', exact: true }).click();
    await page.getByRole('alert').filter({ hasText: /fetch|网络/ }).waitFor();
    const retried = page.waitForResponse(r => new URL(r.url()).pathname === '/api/attach/branch');
    await page.locator('.messages article').first().getByRole('button', { name: '从这里重新开始', exact: true }).click();
    assert.equal((await (await retried).json()).conversation.id, firstBranch);
    await page.getByText('Echo: branch once', { exact: true }).waitFor();
    checks.branch_lost_receipt_reuses_branch = true;

    const policy = await api('attachment-policy'); await api('attachment-policy', { ...policy, max_files: 2 }, 'PUT');
    const uploadChat = await api('chats', { title: 'Attachments fixture' }); await open(uploadChat);
    await input().fill('keep draft');
    await page.locator('input[type=file]').setInputFiles(['a', 'b', 'c'].map(name => ({ name: name + '.txt', mimeType: 'text/plain', buffer: Buffer.from(name) })));
    await page.getByRole('alert').filter({ hasText: '最多选择 2 个附件' }).waitFor();
    assert.equal(await input().inputValue(), 'keep draft');
    assert.equal((await api('attachments?conversation_id=' + uploadChat.id)).length, 0);
    checks.attachment_batch_preflight_keeps_draft = true;
    await page.locator('input[type=file]').setInputFiles([{ name: 'a.txt', mimeType: 'text/plain', buffer: Buffer.from('synthetic attachment') }]);
    await page.getByRole('button', { name: '移除 a.txt', exact: true }).waitFor();
    await input().fill('');
    assert(await send().isDisabled()); assert(await page.getByText('请补充你希望如何处理附件。', { exact: true }).isVisible());
    checks.attachment_only_explanation = true;
    await input().fill('receipt retained');
    await page.route(info.origin + '/api/chats/' + uploadChat.id + '/submit', async route => { await route.fetch(); await route.abort('connectionreset'); }, { times: 1 });
    await send().click();
    await page.getByRole('alert').filter({ hasText: /fetch|网络/ }).waitFor();
    assert.equal(await input().inputValue(), 'receipt retained');
    await send().click();
    await page.waitForFunction(() => document.querySelector('textarea[aria-label="消息"]')?.value === '');
    assert.equal(await page.getByRole('button', { name: '移除 a.txt', exact: true }).count(), 0);
    assert.equal((await api('chats/' + uploadChat.id)).view.turns.length, 1);
    checks.lost_submit_receipt_clears_only_accepted_input = true;

    const b = await api('chats', { title: 'Other draft B' }); await open(b); await input().fill('unfinished B');
    const a = await api('chats', { title: 'Sending A' }); await open(a); await input().fill('send A');
    let received, release; const receipt = new Promise(r => received = r), gate = new Promise(r => release = r);
    await page.route(info.origin + '/api/chats/' + a.id + '/submit', async route => { const response = await route.fetch(); received(); await gate; await route.fulfill({ response }); }, { times: 1 });
    await send().click(); await receipt;
    await page.locator(`[data-chat="${b.id}"] button`).click();
    await input().fill('new edit in B'); release();
    await page.waitForFunction(() => !document.querySelector('textarea[aria-label="消息"]')?.disabled);
    assert.equal(await input().inputValue(), 'new edit in B'); checks.cross_chat_receipt_preserves_draft = true;

    await plan('model.set_profile', { id: 'second', provider: 'echo' });
    await open(a);
    await api('chats/' + a.id + '/model', { model: 'second' });
    await page.getByRole('combobox', { name: '当前模型', exact: true }).filter({ hasText: 'second' }).waitFor();
    await input().fill('use external model'); await send().click();
    await page.getByText('Echo: use external model', { exact: true }).waitFor();
    const modelView = (await api('chats/' + a.id)).view;
    assert.equal(modelView.model_id, 'second'); assert.equal(modelView.turns.at(-1).model_id, 'second');
    checks.external_model_change_not_overwritten = true;

    const intended = await api('chats', { title: 'Loading existing chat' });
    let releaseNav; const navGate = new Promise(r => releaseNav = r);
    await page.route('**/api/chats?*', async route => { const response = await route.fetch(); await navGate; await route.fulfill({ response }); }, { times: 1 });
    await page.goto(info.origin + '/?chat=' + intended.id + '#key=' + key);
    await input().fill('kept during loading'); assert(await send().isDisabled()); releaseNav();
    await page.waitForFunction(() => !document.querySelector('button[aria-label="发送消息"]')?.disabled);
    await send().click(); await page.getByText('Echo: kept during loading', { exact: true }).waitFor();
    assert.equal(new URL(page.url()).searchParams.get('chat'), intended.id);
    assert.equal((await api('chats/' + intended.id)).view.turns.length, 1);
    checks.initial_loading_keeps_target = true;

    const before = (await api('chats/' + intended.id)).view.turns.length;
    await input().fill('/view theme dark'); await send().click();
    await page.waitForFunction(() => document.documentElement.classList.contains('dark'));
    assert.equal((await api('chats/' + intended.id)).view.turns.length, before);
    checks.client_view_command_does_not_create_run = true;
    assert.deepEqual(errors, []);
    fs.mkdirSync(out, { recursive: true });
    fs.writeFileSync(path.join(out, 'interaction.json'), JSON.stringify({ checks, errors }, null, 2));
    await page.screenshot({ path: path.join(out, 'interaction.png') });
    console.log(JSON.stringify(checks));
  } catch (error) {
    fs.mkdirSync(out, { recursive: true });
    fs.writeFileSync(path.join(out, 'interaction-failure.json'), JSON.stringify({ checks, errors, error: String(error) }, null, 2));
    fs.writeFileSync(path.join(out, 'interaction-failure.txt'), await page.locator('body').ariaSnapshot());
    throw error;
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
