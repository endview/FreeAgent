// Real isolated Owner/PG only. No external model or Telegram is contacted.
const fs = require("node:fs"),
  path = require("node:path"),
  assert = require("node:assert/strict");
const { chromium } = require("playwright");
(async () => {
  const root = process.env.FREEAGENT_UI_TEST_ROOT,
    out = process.env.FREEAGENT_UI_ARTIFACTS;
  assert(root && out);
  fs.mkdirSync(out, { recursive: true });
  const info = JSON.parse(
      fs.readFileSync(path.join(root, "data/engine.json"), "utf8"),
    ),
    key = fs.readFileSync(path.join(root, "data/chat.secret"), "utf8").trim();
  const headers = {
    "X-FreeAgent-Key": key,
    Origin: info.origin,
    "Content-Type": "application/json",
  };
  async function api(p, b) {
    const r = await fetch(info.origin + "/api/" + p, {
      headers,
      method: b ? "POST" : "GET",
      body: b ? JSON.stringify(b) : undefined,
    });
    const d = await r.json();
    assert(r.ok, JSON.stringify(d));
    return d;
  }
  async function openConversationMenu(page, title) {
    const trigger = page
      .getByRole("button", { name: "管理会话：" + title })
      .first();
    await trigger.locator("..").hover();
    await trigger.click();
  }
  const chat = await api("chats", {
    title: "回收站真实交互 · 长中文标题与项目整理",
  });
  const browser = await chromium.launch({
    channel: process.env.FREEAGENT_BROWSER_CHANNEL || "msedge",
    headless: true,
  });
  const page = await browser.newPage(),
    errors = [],
    checks = {};
  page.on("pageerror", (e) => errors.push(e.message));
  try {
    await page.goto(info.origin + "/?chat=" + chat.id + "#key=" + key);
    await page
      .getByRole("button", { name: "管理会话：" + chat.title })
      .first()
      .waitFor();
    await openConversationMenu(page, chat.title);
    await page
      .getByRole("menuitem", { name: "移到回收站…", exact: true })
      .click();
    await page.getByRole("button", { name: "预览影响", exact: true }).click();
    await page
      .getByRole("button", { name: "确认移到回收站", exact: true })
      .click();
    await page.getByText("已完成 1 / 1", { exact: true }).waitFor();
    checks.trash = true;
    await page
      .getByRole("button", { name: "关闭", exact: true })
      .first()
      .click();
    await page
      .getByRole("heading", { name: "该会话已移到回收站或永久删除" })
      .waitFor();
    checks.current_chat_disabled = true;
    await page.getByRole("button", { name: "打开回收站", exact: true }).click();
    await page.getByRole("heading", { name: "回收站", exact: true }).waitFor();
    await page
      .getByRole("button", { name: "恢复", exact: true })
      .first()
      .click();
    await page.getByRole("button", { name: "预览影响", exact: true }).click();
    await page
      .getByRole("button", { name: "确认恢复会话", exact: true })
      .click();
    await page.getByText("已完成 1 / 1", { exact: true }).waitFor();
    checks.restore = true;
    await page
      .getByRole("button", { name: "关闭", exact: true })
      .first()
      .click();
    for (const [width, height] of [
      [1440, 900],
      [1024, 768],
      [768, 1024],
      [390, 844],
      [320, 568],
      [844, 390],
    ]) {
      await page.setViewportSize({ width, height });
      await page.goto(info.origin + "/?view=manage#key=" + key);
      await page
        .getByRole("heading", { name: "空间内容", exact: true })
        .waitFor();
      const p = page.locator(".management-page");
      await page.waitForFunction(
        () => !!document.querySelector(".management-row"),
      );
      const overflow = await p.evaluate(
        (e) => e.scrollWidth > e.clientWidth + 1,
      );
      assert(!overflow, `management overflow ${width}`);
      await page.screenshot({
        path: path.join(out, `management-${width}x${height}.png`),
      });
    }
    checks.responsive = true;
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(info.origin + "/?chat=" + chat.id + "#key=" + key);
    const row = page.locator(`[data-chat="${chat.id}"]`);
    await row.waitFor();
    await row.click({ button: "right" });
    await page.getByRole("menuitem", { name: "重命名", exact: true }).waitFor();
    checks.right_click = true;
    await page.keyboard.press("Escape");
    await row.locator("button.conversation").focus();
    await page.keyboard.press("Shift+F10");
    await page
      .getByRole("menuitem", { name: "重命名", exact: true })
      .waitFor({ timeout: 2500 });
    checks.keyboard_menu = true;
    await page.keyboard.press("Escape");
    await openConversationMenu(page, chat.title);
    await page.getByRole("menuitem", { name: "重命名", exact: true }).click();
    const input = page.getByRole("textbox", { name: "会话名称", exact: true });
    await input.fill("新的会话名称");
    await input.press("Escape");
    checks.rename_escape = true;
    await openConversationMenu(page, chat.title);
    await page.getByRole("menuitem", { name: "重命名", exact: true }).click();
    await input.fill("已经保存的名称");
    await page
      .getByRole("button", { name: "保存会话名称", exact: true })
      .click();
    await page
      .getByRole("button", { name: "管理会话：已经保存的名称" })
      .first()
      .waitFor();
    checks.inline_rename_saved = true;
    await openConversationMenu(page, "已经保存的名称");
    await page.getByRole("menuitem", { name: "重命名", exact: true }).click();
    await input.fill("保留冲突草稿");
    let cp = await api("conversation-management", {
      operation: "preview",
      command: { action: "rename", ids: [chat.id], title: "另一窗口的标题" },
    });
    await api("conversation-management", { operation: "execute", id: cp.id });
    await page
      .getByRole("button", { name: "保存会话名称", exact: true })
      .click();
    await page.locator(".conversation-rename-error").waitFor();
    assert.equal(await input.inputValue(), "保留冲突草稿");
    checks.rename_conflict_retains_draft = true;
    await input.press("Escape");
    // A second browser client retires this chat; the first must stop presenting a composer.
    const other = await browser.newPage();
    await other.goto(info.origin + "/?chat=" + chat.id + "#key=" + key);
    await openConversationMenu(other, "另一窗口的标题");
    await other
      .getByRole("menuitem", { name: "移到回收站…", exact: true })
      .click();
    await other.getByRole("button", { name: "预览影响", exact: true }).click();
    await other
      .getByRole("button", { name: "确认移到回收站", exact: true })
      .click();
    await page
      .getByRole("heading", { name: "该会话已移到回收站或永久删除" })
      .waitFor();
    checks.multiple_clients = true;
    await other.close();
    cp = await api("conversation-management", {
      operation: "preview",
      command: { action: "restore", ids: [chat.id] },
    });
    await api("conversation-management", { operation: "execute", id: cp.id });
    await page.goto(info.origin + "/?chat=" + chat.id + "#key=" + key);
    await openConversationMenu(page, "另一窗口的标题");
    await page
      .getByRole("menuitem", { name: "移到空间…", exact: true })
      .click();
    await page
      .getByRole("textbox", { name: "新建普通空间并移入" })
      .fill("归档工作");
    await page.getByRole("button", { name: "创建并选中", exact: true }).click();
    await page
      .getByRole("combobox", { name: "目标空间", exact: true })
      .filter({ hasText: "归档工作" })
      .waitFor();
    await page.getByRole("button", { name: "预览影响", exact: true }).click();
    await page
      .getByRole("button", { name: "确认移到空间", exact: true })
      .click();
    await page.getByText("已完成 1 / 1", { exact: true }).waitFor();
    await page
      .getByRole("button", { name: "关闭", exact: true })
      .first()
      .click();
    checks.create_space_and_move = true;
    const spaces = await api("spaces"),
      space = spaces.find((s) => s.title === "归档工作");
    assert(space);
    await api("chats", { title: "批量保留", space_id: space.id });
    await api("chats", { title: "批量测试", space_id: space.id });
    await page.goto(
      info.origin + "/?view=manage&space=" + space.id + "#key=" + key,
    );
    await page.getByText("3 个会话", { exact: true }).waitFor();
    await page
      .getByRole("button", { name: "选择全部筛选结果", exact: true })
      .click();
    await page.getByText("已选 3", { exact: true }).waitFor();
    await page.getByRole("combobox", { name: "排序", exact: true }).click();
    await page.getByRole("option", { name: "轮次", exact: true }).click();
    await page.getByText("已选 3", { exact: true }).waitFor();
    checks.sort_preserves_selection = true;
    await page
      .locator(".management-batch")
      .getByRole("button", { name: "移到回收站", exact: true })
      .click();
    await page.getByRole("button", { name: "预览影响", exact: true }).click();
    await page
      .getByRole("button", { name: "确认移到回收站", exact: true })
      .click();
    await page.getByText("已完成 3 / 3", { exact: true }).waitFor();
    await page
      .getByRole("button", { name: "关闭", exact: true })
      .first()
      .click();
    checks.batch_selection = true;
    // Settings and menus must use the same explicit default when deleting a space.
    const created = await api("spaces", {
      kind: "space.create",
      arguments: { title: "设置页删除测试" },
      confirmed: true,
    });
    const inside = await api("chats", {
      title: "默认勾选移入回收站",
      space_id: created.data.id,
    });
    await page.goto(info.origin + "/?view=settings&section=spaces#key=" + key);
    const editor = page.locator(".settings-section").filter({
      has: page.getByRole("heading", { name: "设置页删除测试", exact: true }),
    });
    await editor.getByRole("button", { name: "删除空间", exact: true }).click();
    assert(
      await page
        .getByRole("checkbox", {
          name: "同时将其中的会话移到回收站",
          exact: true,
        })
        .isChecked(),
    );
    await page.getByRole("button", { name: "预览影响", exact: true }).click();
    await page
      .getByRole("button", { name: "确认删除空间", exact: true })
      .click();
    await page.getByText("已完成 1 / 1", { exact: true }).waitFor();
    await page
      .getByRole("button", { name: "关闭", exact: true })
      .first()
      .click();
    const st = await api("conversation-management?status=" + inside.id);
    assert.equal(st[inside.id].state, "trashed");
    checks.delete_space_default = true;
    await page.goto(info.origin + "/?view=manage#key=" + key);
    await page
      .getByRole("heading", { name: "空间内容", exact: true })
      .waitFor();
    await page.getByRole("button", { name: "切换主题", exact: true }).click();
    if (
      !(await page
        .locator("html")
        .evaluate((e) => e.classList.contains("dark")))
    )
      await page.getByRole("button", { name: "切换主题", exact: true }).click();
    await page.waitForFunction(() =>
      document.documentElement.classList.contains("dark"),
    );
    await page.screenshot({
      path: path.join(out, "management-dark-1440x900.png"),
    });
    await page.addStyleTag({ content: "html{zoom:2}" });
    const overflow = await page
      .locator(".management-page")
      .evaluate((e) => e.scrollWidth > e.clientWidth + 1);
    assert(!overflow, "management overflow at CSS 200%");
    await page.screenshot({
      path: path.join(out, "management-200-percent.png"),
    });
    checks.dark_and_scaled = true;
    const nativeChat = await api("chats", { title: "桌面编辑菜单隔离" });
    const native = await browser.newPage();
    native.on("pageerror", (e) => errors.push(e.message));
    await native.addInitScript(() => {
      window.faOpenExternal = async () => {};
    });
    await native.goto(info.origin + "/?chat=" + nativeChat.id + "#key=" + key);
    const nativeRow = native.locator(`[data-chat="${nativeChat.id}"]`);
    await nativeRow.waitFor();
    await nativeRow.click({ button: "right" });
    await native
      .getByRole("menuitem", { name: "重命名", exact: true })
      .waitFor();
    assert.equal(await native.getByRole("menu").count(), 1);
    await native.keyboard.press("Escape");
    await nativeRow.hover();
    await nativeRow.locator(".conversation-more").click();
    await native.getByRole("menuitem", { name: "重命名", exact: true }).click();
    const nativeInput = native.getByRole("textbox", {
      name: "会话名称",
      exact: true,
    });
    await nativeInput.click({ button: "right" });
    await native.getByRole("menu", { name: "编辑菜单", exact: true }).waitFor();
    await native.getByRole("menuitem", { name: "全选", exact: true }).click();
    assert(
      await nativeInput.evaluate(
        (e) => e.selectionStart === 0 && e.selectionEnd === e.value.length,
      ),
    );
    checks.desktop_binding_menu_routing = true;
    await native.close();
    const purge = await api("conversation-management", {
      operation: "preview",
      command: { action: "purge", ids: [chat.id] },
    });
    await api("conversation-management", {
      operation: "execute",
      id: purge.id,
    });
    checks.purge = true;
    checks.no_runtime_errors = !errors.length;
    fs.writeFileSync(
      path.join(out, "management-results.json"),
      JSON.stringify({ checks, errors }, null, 2),
    );
    console.log(JSON.stringify({ checks, errors }));
    assert(Object.values(checks).every(Boolean));
  } catch (e) {
    await page.screenshot({ path: path.join(out, "management-failure.png") });
    throw e;
  } finally {
    await browser.close();
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
