// modes.ts: helpers for specs that reach into the side pane (the app opens in Agent mode).
import { expect, type Page } from "@playwright/test";

// showPane opens the side pane when it is closed. Every spec that reaches into
// the pane needs it there first.
export async function showPane(page: Page): Promise<void> {
  if (await page.locator("#sidePane").isHidden()) await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  // + also offers what to put in the pane; a spec that just wants the pane
  // open dismisses that menu so it cannot swallow the next click.
  if (await page.locator("#paneHistoryMenu").isVisible()) await page.keyboard.press("Escape");
}

// hidePane closes the pane, giving the conversation the whole card. This is
// what the old "tabs" layout used to mean.
export async function hidePane(page: Page): Promise<void> {
  if (await page.locator("#sidePane").isVisible()) {
    const close = page.locator("#paneCloseBtn");
    if (await close.isVisible()) await close.click();
    else await page.locator("#paneNewChatBtn").click();
  }
  await expect(page.locator("#sidePane")).toBeHidden();
}

// addToPane opens the pane and adds one thing to it, whichever way that has to
// happen: the picker when the pane is empty, the strip's own + when it already
// holds something. Specs should not care which — that is the UI's business.
export async function addToPane(page: Page, kind: "chat" | "terminal"): Promise<void> {
  if (await page.locator("#sidePane").isHidden()) await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  const pick = page.locator(`#panePicker [data-pane-pick="${kind}"]`);
  if (await pick.isVisible()) {
    await pick.click();
    return;
  }
  await page.locator("#paneTabsAdd").click();
  await page.getByRole("menuitem", { name: kind === "chat" ? "New side chat" : "New terminal" }).click();
}
