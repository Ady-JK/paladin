// popovers.spec.ts checks the composer popovers are mutually exclusive:
// opening one closes whichever other one is open.
import { test, expect, type Page } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await expect(page.locator("#promptInput")).toBeVisible();
});

const menus = ["#permMenu", "#toolsMenu", "#budgetMenu", "#modelMenu"];

async function onlyOpen(page: Page, open: string): Promise<void> {
  for (const m of menus) {
    if (m === open) await expect(page.locator(m)).toBeVisible();
    else await expect(page.locator(m)).toBeHidden();
  }
}

test("opening a composer popover closes the others", async ({ page }) => {
  await page.click("#permChip");
  await onlyOpen(page, "#permMenu");
  await page.click("#toolsChip");
  await onlyOpen(page, "#toolsMenu");
  await page.click("#budgetChip");
  await onlyOpen(page, "#budgetMenu");
  await page.click("#modelSelectBtn");
  await onlyOpen(page, "#modelMenu");
  await page.click("#permChip");
  await onlyOpen(page, "#permMenu");
});

test("the thinking card is exclusive with the other popovers", async ({ page }) => {
  await page.click("#modelSelectBtn");
  await page.fill("#modelSearch", "claude-3-opus");
  await page.waitForTimeout(250);
  await page.press("#modelSearch", "Enter");
  await page.click("#budgetChip");
  await expect(page.locator("#budgetMenu")).toBeVisible();
  await page.click("#thinkingChip");
  await expect(page.locator("#thinkingMenu")).toBeVisible();
  await expect(page.locator("#budgetMenu")).toBeHidden();
  await page.click("#toolsChip");
  await expect(page.locator("#thinkingMenu")).toBeHidden();
  await expect(page.locator("#toolsMenu")).toBeVisible();
});
