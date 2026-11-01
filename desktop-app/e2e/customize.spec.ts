// customize.spec.ts covers Settings → Customize: the grouped list, the
// editor with placeholders and its file, saving, invalid input, reset,
// unsaved-change protection, search and the skill switch.
import { test, expect, type Page } from "@playwright/test";

async function openCustomize(page: Page) {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.click("#settingsBtn");
  await page.locator("#settingsNavCustomize").click();
  await expect(page.locator("#settingsPaneTitle")).toHaveText("Customize");
  return page.locator("#customizeBody");
}

const calls = (page: Page) => page.evaluate(() => (window as any).__e2eCustomCalls ?? []);

test("built-ins are listed by group and open in the editor", async ({ page }) => {
  const pane = await openCustomize(page);
  await expect(pane.locator(".cz-group")).toHaveText([
    "Tool guidance",
    "Tool descriptions",
    "Reminders",
    "Skills",
    "Settings files",
  ]);
  await pane.locator('[data-cz-item="reminder.verify"]').click();
  await expect(pane.locator(".cz-title")).toContainText("Check before finishing");
  await expect(pane.locator(".cz-vars code")).toHaveText("{{files}}");
  await expect(pane.locator(".cz-path")).toContainText("/data/config/custom/reminder.verify.md");
  await expect(pane.locator(".cz-text")).toHaveValue("Check {{files}}.");
  await expect(pane.locator("[data-cz-reset]")).toBeDisabled();
});

test("saving marks the item edited, warns about a dropped placeholder, and reset restores it", async ({ page }) => {
  const pane = await openCustomize(page);
  await pane.locator('[data-cz-item="reminder.verify"]').click();
  await pane.locator(".cz-text").fill("Run the tests before you stop.");
  await expect(pane.locator(".cz-status")).toHaveText("Unsaved changes");
  await pane.locator("[data-cz-save]").click();
  await expect(pane.locator(".cz-status")).toHaveText("Saved. Applies from the next turn.");
  await expect(pane.locator(".cz-warn")).toContainText("{{files}}");
  await expect(pane.locator('[data-cz-item="reminder.verify"] .cz-badge')).toHaveText("edited");
  expect(await calls(page)).toContainEqual({
    cmd: "customSet",
    id: "reminder.verify",
    text: "Run the tests before you stop.",
  });
  // Reset asks once more before it deletes the override.
  await pane.locator("[data-cz-reset]").click();
  await expect(pane.locator("[data-cz-reset]")).toHaveText("Confirm reset");
  await pane.locator("[data-cz-reset]").click();
  await expect(pane.locator(".cz-text")).toHaveValue("Check {{files}}.");
  await expect(pane.locator('[data-cz-item="reminder.verify"] .cz-badge')).toHaveCount(0);
});

test("invalid JSON is refused with the reason, and unsaved edits are not lost by a click", async ({ page }) => {
  const pane = await openCustomize(page);
  await pane.locator('[data-cz-item="file.settings"]').click();
  await pane.locator(".cz-text").fill("{ not json");
  await pane.locator("[data-cz-save]").click();
  await expect(pane.locator(".cz-status")).toHaveText(/must be a JSON object/);
  await expect(pane.locator('[data-cz-item="file.settings"] .cz-badge')).toHaveCount(0);
  // Picking another item asks first.
  await pane.locator('[data-cz-item="tool.read.description"]').click();
  await expect(pane.locator(".cz-dirty")).toBeVisible();
  await pane.locator("[data-cz-keep]").click();
  await expect(pane.locator(".cz-text")).toHaveValue("{ not json");
  await pane.locator('[data-cz-item="tool.read.description"]').click();
  await pane.locator("[data-cz-discard]").click();
  await expect(pane.locator(".cz-text")).toHaveValue("Read a file.");
});

test("search filters the list and a skill switches off", async ({ page }) => {
  const pane = await openCustomize(page);
  await pane.locator(".cz-search").fill("debug");
  await expect(pane.locator(".cz-item")).toHaveCount(1);
  await pane.locator('[data-cz-item="skill.debugging"]').click();
  const sw = pane.locator("[data-cz-skill]");
  await expect(sw).toHaveAttribute("aria-checked", "true");
  await sw.click();
  await expect(sw).toHaveAttribute("aria-checked", "false");
  await expect(pane.locator('[data-cz-item="skill.debugging"] .cz-badge')).toHaveText("off");
  expect(await calls(page)).toContainEqual({ cmd: "skillEnabledSet", name: "debugging", enabled: false });
  await pane.locator(".cz-search").fill("zzz");
  await expect(pane.locator(".cz-none")).toContainText("Nothing matches");
});

test("an unsaved draft survives other tabs and closing Settings", async ({ page }) => {
  const pane = await openCustomize(page);
  await pane.locator('[data-cz-item="reminder.verify"]').click();
  await pane.locator(".cz-text").fill("Draft I have not saved");
  await page.locator("#settingsNavWorkspace").click();
  await page.locator("#settingsNavCustomize").click();
  await expect(pane.locator(".cz-text")).toHaveValue("Draft I have not saved");
  await page.click("#settingsCloseBtn");
  await page.click("#settingsBtn");
  await page.locator("#settingsNavCustomize").click();
  await expect(pane.locator(".cz-text")).toHaveValue("Draft I have not saved");
  await expect(pane.locator(".cz-status")).toHaveText("Unsaved changes");
  await pane.locator("[data-cz-revert]").click();
  await expect(pane.locator(".cz-text")).toHaveValue("Check {{files}}.");
});

test("text typed while Customize loads is never overwritten", async ({ page }) => {
  const pane = await openCustomize(page);
  await pane.locator('[data-cz-item="reminder.verify"]').click();
  await expect(pane.locator(".cz-text")).toHaveValue("Check {{files}}.");
  await page.evaluate(() => ((window as any).__e2eCustomDelay = 400));
  // A refresh of the list (coming back to the tab) is still loading.
  await page.locator("#settingsNavWorkspace").click();
  await page.locator("#settingsNavCustomize").click();
  await pane.locator(".cz-text").fill("Typed during the refresh");
  await page.waitForTimeout(800);
  await expect(pane.locator(".cz-text")).toHaveValue("Typed during the refresh");
  // Another item is still loading when the user types in this one.
  await pane.locator('[data-cz-item="tool.read.description"]').click();
  await pane.locator(".cz-text").fill("Typed while the next item loads");
  await page.waitForTimeout(800);
  await expect(pane.locator(".cz-text")).toHaveValue("Typed while the next item loads");
  await expect(pane.locator(".cz-dirty")).toBeVisible();
  await pane.locator("[data-cz-discard]").click();
  await expect(pane.locator(".cz-text")).toHaveValue("Read a file.");
});
