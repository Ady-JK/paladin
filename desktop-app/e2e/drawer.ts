// drawer.ts: the sidebar is a drawer that closes on outside clicks and
// session picks, so specs reopen it before reaching into it.
import { expect, type Page } from "@playwright/test";

export async function showSidebar(page: Page): Promise<void> {
  // The drawer is a column now and never closes itself, so the state is
  // whatever the test left behind: only open it when it is shut.
  if (await page.locator("#sidebar.is-closed").count()) {
    await page.click("#agentTab");
  }
  await expect(page.locator("#sidebar:not(.is-closed)")).toBeVisible();
}
