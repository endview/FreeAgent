// Actual HTTPS, authenticated Core, and isolated DB. No Telegram requests.
const { chromium } = require("playwright");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

(async () => {
  const origin = process.env.FREEAGENT_PUBLIC_SEARCH_ORIGIN;
  const artifacts = process.env.FREEAGENT_PUBLIC_SEARCH_ARTIFACTS;
  assert(origin && artifacts);
  fs.mkdirSync(artifacts, { recursive: true });
  const browser = await chromium.launch({
    headless: true,
    channel: process.env.FREEAGENT_BROWSER_CHANNEL || "chrome",
  });
  const contexts = [];
  const errors = [];
  async function login(section) {
    const context = await browser.newContext({
      ignoreHTTPSErrors: true, // Only the fixture's self-signed certificate.
      viewport: { width: 1440, height: 960 },
    });
    contexts.push(context);
    const page = await context.newPage();
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(origin + "/?view=settings&section=" + section);
    await page.getByLabel("管理员密码", { exact: true }).fill("public fixture only password");
    await page.getByRole("button", { name: "进入工作台", exact: true }).click();
    return page;
  }
  try {
    const a = await login("context");
    const b = await login("context");
    const field = (page) => page.getByLabel("自动触发占比（%）", { exact: true });
    const save = (page) => page.getByRole("button", { name: "保存设置", exact: true });
    await field(a).fill("76");
    await field(b).fill("74");
    let response = a.waitForResponse((r) => r.url().endsWith("/api/settings/context") && r.request().method() === "PATCH");
    await save(a).click();
    assert((await response).ok());
    response = b.waitForResponse((r) => r.url().endsWith("/api/settings/context") && r.request().method() === "PATCH");
    await save(b).click();
    assert.equal((await response).status(), 409);
    assert.equal(await field(b).inputValue(), "74");
    await a.reload();
    assert.equal(await field(a).inputValue(), "76");

    await a.goto(origin + "/?view=settings&section=channels");
    await b.goto(origin + "/?view=settings&section=channels");
    const replace = (page) => page.getByRole("button", { name: "替换 Token", exact: true });
    const secret = (page) => page.getByLabel("Bot Token / 应用 Secret", { exact: true });
    const rotate = (page) => page.getByRole("button", { name: "保存新 Token", exact: true });
    await a.getByRole("heading", { name: "Telegram 私聊管理绑定", exact: true }).waitFor();
    await replace(b).click();
    await b.getByLabel("凭据名称", { exact: true }).fill("Old draft name");
    await secret(b).fill("1:fixture-stale-token-123");
    await replace(a).click();
    await secret(a).fill("1:fixture-rotated-token-456");
    response = a.waitForResponse((r) => r.url().endsWith("/api/channels/credentials") && r.request().method() === "POST");
    await rotate(a).click();
    assert((await response).ok());
    response = b.waitForResponse((r) => r.url().endsWith("/api/channels/credentials") && r.request().method() === "POST");
    await rotate(b).click();
    assert.equal((await response).status(), 409);
    await b.getByRole("button", { name: "使用最新版本重新编辑" }).waitFor();
    assert.equal(await secret(b).inputValue(), "");
    assert.equal(await b.getByLabel("凭据名称", { exact: true }).inputValue(), "Old draft name");
    assert.equal(await b.evaluate(() => JSON.stringify(sessionStorage).includes("fixture-stale-token")), false);

    const binding = a.getByRole("region", { name: "Telegram 私聊管理绑定", exact: true });
    await binding.getByRole("status").filter({ hasText: "未绑定" }).waitFor();
    await binding.getByRole("combobox", { name: "已核实的私聊用户 ID", exact: true }).click();
    await a.getByRole("option", { name: "7", exact: true }).click();
    await binding.getByRole("button", { name: "保存绑定", exact: true }).click();
    await binding.getByRole("status").filter({ hasText: "已绑定" }).waitFor();
    await a.reload();
    await binding.getByRole("status").filter({ hasText: "已绑定" }).waitFor();
    for (const width of [1440, 768, 390, 320]) {
      await a.setViewportSize({ width, height: 900 });
      await binding.scrollIntoViewIfNeeded();
      assert(await a.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "viewport overflow: " + width);
      assert(await binding.evaluate((el) => el.scrollWidth <= el.clientWidth + 1), "binding overflow: " + width);
      await a.screenshot({ path: path.join(artifacts, "telegram-binding-" + width + ".png") });
    }
    await binding.getByRole("button", { name: "暂停", exact: true }).click();
    await binding.getByRole("status").filter({ hasText: /^已暂停 ·/ }).waitFor();
    a.once("dialog", (dialog) => void dialog.accept());
    await binding.getByRole("button", { name: "解除绑定", exact: true }).click();
    await binding.getByRole("status").filter({ hasText: "未绑定" }).waitFor();
    assert.deepEqual(errors, []);
    console.log("HTTPS context save/reload, stale context draft, token rotation/conflict, binding/pause/revoke and 4 widths passed.");
  } catch (error) {
    const page = contexts.at(-1)?.pages()[0];
    if (page) await page.screenshot({ path: path.join(artifacts, "admin-settings-failure.png") }).catch(() => {});
    throw error;
  } finally {
    await browser.close();
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
