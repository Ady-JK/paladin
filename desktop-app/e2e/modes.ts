// modes.ts: the app opens in Agent mode, so a spec that describes Browser or
// IDE has to ask for that mode. Switching from a known state (rather than
// clicking blindly) means a spec does not care where the app started, and the
// mode is remembered across a reload, so a test that reloads keeps it.
import { expect, type Page } from "@playwright/test";

export type AppMode = "agent" | "browser" | "ide";

// useMode puts the app in `mode`, clicking only when it is not there yet.
export async function useMode(page: Page, mode: AppMode): Promise<void> {
  const button = page.locator(
    mode === "agent" ? "#modeAgentBtn" : mode === "browser" ? "#modeBrowserBtn" : "#ideModeBtn",
  );
  const pressed = mode === "ide" ? await button.getAttribute("aria-pressed") : null;
  const selected = await button.getAttribute("aria-selected");
  const active = mode === "ide" ? pressed === "true" : selected === "true";
  if (!active) await button.click();
  if (mode === "ide") await expect(button).toHaveAttribute("aria-pressed", "true");
  else await expect(button).toHaveAttribute("aria-selected", "true");
  // The views cross-fade and the pane animates its width, so geometry read
  // straight after a switch would be mid-transition.
  await page.waitForTimeout(300);
}

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
