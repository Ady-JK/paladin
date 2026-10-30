// drawer.ts: the sidebar is a drawer that closes on outside clicks and
// session picks, so specs reopen it before reaching into it.
import { expect, type Page } from "@playwright/test";

export async function showSidebar(page: Page): Promise<void> {
  // A session pick closes the drawer 250ms later; let that settle first.
  await page.waitForTimeout(300);
  if (await page.locator("#sidebar.is-closed").count()) {
    await page.click("#agentTab");
  }
  await expect(page.locator("#sidebar:not(.is-closed)")).toBeVisible();
}
