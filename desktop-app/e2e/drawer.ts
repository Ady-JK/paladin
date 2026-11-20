// drawer.ts: the sidebar is a drawer that closes on outside clicks and
// session picks, so specs reopen it before reaching into it.
import { expect, type Page } from "@playwright/test";

export async function showSidebar(page: Page): Promise<void> {
  // The drawer is a column now and never closes itself, so the state is
  // whatever the test left behind: only open it when it is shut.
  if (await page.locator("#sidebar.is-closed").count()) {
    await page.click("#agentTab");
  }
  // Wait for the column to finish animating its width: it narrows the card
  // as it opens, so a spec that measures geometry right after this call
  // would otherwise read a mid-transition layout.
  await expect
    .poll(async () => (await page.locator("#sidebar").boundingBox())?.width ?? 0, { timeout: 4000 })
    .toBeGreaterThan(300);
}
