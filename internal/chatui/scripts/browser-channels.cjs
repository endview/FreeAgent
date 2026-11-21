// Isolated real Owner/PG/HTTP protocol and real browser; no production bot.
const { chromium } = require("playwright");
const fs = require("node:fs"),
  path = require("node:path"),
  assert = require("node:assert/strict");
(async () => {
  const root = process.env.FREEAGENT_UI_TEST_ROOT,
    artifacts = process.env.FREEAGENT_UI_ARTIFACTS;
  if (!root || !artifacts)
    throw Error("Use test-frontend.py with an isolated Core");
  fs.mkdirSync(artifacts, { recursive: true });
  const data = path.join(root, "data"),
    info = JSON.parse(fs.readFileSync(path.join(data, "engine.json"), "utf8")),
    key = fs.readFileSync(path.join(data, "chat.secret"), "utf8").trim();
  async function api(route, body, method) {
    const r = await fetch(info.origin + "/api/" + route, {
      method: method || (body ? "POST" : "GET"),
      headers: {
        "X-FreeAgent-Key": key,
        Origin: info.origin,
        "Content-Type": "application/json",
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    const v = await r.json();
    assert(r.ok, route + ": " + r.status + " " + JSON.stringify(v));
    return v;
  }
  await api("channels/credentials", {
    name: "Synthetic channel",
    protocol: "telegram",
    base_url: "http://127.0.0.1:1",
    source: "saved",
    secret: "1:ISOLATED_FIXTURE_ONLY",
  });
  const credential = (await api("channels/credentials")).find(
    (item) => item.name === "Synthetic channel",
  );
  assert(credential);
  const config = await api("channels");
  await api(
    "channels",
    {
      connections: [
        {
          id: "fixture",
          source: "telegram",
          application_id: "1",
          credential: credential.id,
          enabled: true,
        },
      ],
      expected_revisions: config.expected_revisions,
    },
    "PATCH",
  );
  const boot = JSON.parse(
    fs.readFileSync(path.join(data, "channel-runtime", "fixture.json"), "utf8"),
  );
  assert.equal(boot.version, 4);
  async function observe(body) {
    const r = await fetch(info.origin + "/api/channel-plugin/v1/observation", {
      method: "POST",
      headers: {
        Authorization: "Bearer " + boot.token,
        "X-FreeAgent-Core": boot.core_id,
        "X-FreeAgent-Generation": boot.generation,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
    });
    assert.equal(r.status, 200);
  }
  await observe({ stage: "connected", count: 1, can_read_all: false });
  await observe({
    stage: "received",
    count: 2,
    target: { source: "telegram", application_id: "1", chat_id: "-100" },
  });
  const groupTarget = {
    source: "telegram",
    application_id: "1",
    chat_id: "-100",
  };
  await api("channels/telegram/access", {
    target: groupTarget,
    kind: "group",
    enabled: true,
    members: ["telegram:user:7"],
    whole_group: false,
  });
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.FREEAGENT_BROWSER_CHANNEL
      ? { channel: process.env.FREEAGENT_BROWSER_CHANNEL }
      : {}),
  });
  const page = await browser.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  try {
    await page.goto(
      info.origin + "/?view=settings&section=channels#key=" + key,
    );
    await page.getByText("接收与投递诊断", { exact: true }).click();
    await page.getByText("聊天 -100", { exact: true }).click();
    await observe({
      stage: "received",
      count: 1,
      target: { source: "telegram", application_id: "1", chat_id: "-100" },
    });
    await page.getByRole("button", { name: "刷新诊断", exact: true }).click();
    await page
      .locator(".channel-diagnostics div")
      .filter({ hasText: "收到平台更新" })
      .first()
      .getByText("3", { exact: true })
      .waitFor();
    assert(
      (
        await page
          .getByText("全局隐私模式：已开启", { exact: false })
          .textContent()
      ).includes("不会授予工具权限"),
    );
    const confirmers = page.getByRole("textbox", {
      name: "设置确认人员（每行一个 TG 数字用户 ID）",
      exact: true,
    });
    await confirmers.fill("7\n9");
    await page
      .getByRole("button", { name: "保存确认名单", exact: true })
      .click();
    await page
      .getByRole("status")
      .filter({ hasText: "确认名单已保存" })
      .waitFor();
    let people = (await api("channels/telegram/confirmers")).bots[0].state;
    assert.equal(people.revision, 1);
    assert.deepEqual(people.members, ["telegram:user:7", "telegram:user:9"]);
    await confirmers.fill("7\n8");
    await api("channels/telegram/confirmers", {
      bot: "1",
      members: ["7"],
      expected_revision: 1,
      operation_id: "another-owner-update",
    });
    const stale = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/channels/telegram/confirmers" &&
        r.request().method() === "POST",
    );
    await page
      .getByRole("button", { name: "保存确认名单", exact: true })
      .click();
    assert.equal((await stale).status(), 409);
    assert.equal(await confirmers.inputValue(), "7\n8");
    await page
      .getByRole("button", { name: "放弃草稿并读取当前名单", exact: true })
      .click();
    await page
      .getByRole("status")
      .filter({ hasText: "已读取保存的名单" })
      .waitFor();
    assert.equal(await confirmers.inputValue(), "7");
    await page.getByText("群回复与背景", { exact: true }).click();
    const groupPanel = page
      .locator("details")
      .filter({
        has: page.locator("summary").getByText("群回复与背景", { exact: true }),
      });
    const replyAll = groupPanel.getByRole("checkbox").nth(0),
      background = groupPanel.getByRole("checkbox").nth(1);
    assert.equal(await replyAll.isChecked(), false);
    assert.equal(await background.isChecked(), false);
    await replyAll.check();
    await background.check();
    await groupPanel
      .getByRole("button", { name: "保存群设置", exact: true })
      .click();
    await groupPanel
      .getByRole("status")
      .filter({ hasText: "群设置已保存" })
      .waitFor();
    const groupRoute = "channels/telegram/groups?bot=1&chat=-100";
    let groupState = (await api(groupRoute)).state;
    assert.equal(groupState.reply_all, true);
    assert.equal(groupState.save_background, true);
    await background.uncheck();
    await api("channels/telegram/groups", {
      target: groupTarget,
      action: "save",
      reply_all: false,
      save_background: true,
      expected_revision: groupState.revision,
      operation_id: "concurrent-group-change",
    });
    const groupConflict = page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === "/api/channels/telegram/groups" &&
        r.request().method() === "POST",
    );
    await groupPanel
      .getByRole("button", { name: "保存群设置", exact: true })
      .click();
    assert.equal((await groupConflict).status(), 409);
    assert.equal(await background.isChecked(), false);
    assert.equal(await replyAll.isChecked(), true);
    await groupPanel
      .getByRole("button", { name: "放弃草稿并读取当前值", exact: true })
      .click();
    await groupPanel
      .getByRole("status")
      .filter({ hasText: "已读取当前群设置" })
      .waitFor();
    assert.equal(await replyAll.isChecked(), false);
    assert.equal(await background.isChecked(), true);
    for (const [width, height] of [
      [1440, 1024],
      [768, 1024],
      [390, 844],
    ]) {
      await page.setViewportSize({ width, height });
      await confirmers.scrollIntoViewIfNeeded();
      assert(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth + 1,
        ),
        "page overflow at " + width,
      );
      await page.screenshot({
        path: path.join(artifacts, "channels-" + width + ".png"),
        fullPage: true,
      });
      await confirmers.locator("xpath=ancestor::form[1]").screenshot({
        path: path.join(artifacts, "confirmers-" + width + ".png"),
      });
      await groupPanel.scrollIntoViewIfNeeded();
      await groupPanel.screenshot({
        path: path.join(artifacts, "group-policy-" + width + ".png"),
      });
    }
    assert.equal(errors.length, 0);
    console.log(
      JSON.stringify({
        diagnostic_refresh: true,
        privacy: true,
        group_scope: true,
        confirmers_save: true,
        conflict_preserves_draft: true,
        explicit_readback: true,
        group_policy_save_conflict_readback: true,
        widths: [1440, 768, 390],
        errors,
      }),
    );
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
