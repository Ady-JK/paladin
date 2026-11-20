// layout.ts: the app opens in the tabs layout, so a spec that describes the
// split has to ask for it. Toggling from a known state (rather than clicking
// blindly) means a spec does not care which layout the app started in, and
// the layout choice is remembered across a reload, so a test that reloads
// keeps the layout it asked for.
import { expect, type Page } from "@playwright/test";

export type Layout = "split" | "tabs";

// useLayout puts the app in `layout`, clicking only when it is not there yet.
export async function useLayout(page: Page, layout: Layout): Promise<void> {
  const button = page.locator("#paneLayoutBtn");
  const current = await button.getAttribute("data-layout");
  if (current !== layout) await button.click();
  await expect(button).toHaveAttribute("data-layout", layout);
  await expect(page.locator("#agentView")).toHaveClass(layout === "tabs" ? /is-tab-layout/ : /^(?!.*is-tab-layout).*$/);
  // The tab list moves between the pane's strip and the shell drawer, and both
  // animate their width. Measuring geometry straight after the switch would
  // read a mid-transition layout, so wait for the move to settle.
  await page.waitForTimeout(300);
}
