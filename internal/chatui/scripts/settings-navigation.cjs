// Open a real settings item even when its approved group is collapsed.
async function openSettingsPage(page, name) {
  const item = page.locator('.settings-nav-item').filter({
    hasText: new RegExp('^' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$'),
  });
  const toggle = item.locator('xpath=ancestor::section[1]').locator('.settings-group-toggle');
  if (await toggle.getAttribute('aria-expanded') === 'false') await toggle.click();
  await item.click();
}
module.exports = { openSettingsPage };
