// layoutDefaults.spec.ts pins four things about where things sit, all of which
// were wrong in ways you notice without being able to name: the picker pinned
// to the bottom of an empty pane, Agent mode coming back from the IDE at half
// width with nothing to show, a dropdown competing with the pane's own picker,
// and window controls sitting a few pixels above the icons beside them.
import { expect, test, type Page } from "@playwright/test";

async function start(page: Page): Promise<void> {
  await page.setViewportSize({ width: 1536, height: 864 });
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.addInitScript(() => localStorage.removeItem("paladin.appMode"));
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("hello");
}

const centreY = (page: Page, selector: string): Promise<number> =>
  page.evaluate((sel) => {
    const el = document.querySelector(sel);
    if (!el) return 0;
    const r = el.getBoundingClientRect();
    return Math.round(r.top + r.height / 2);
  }, selector);

test("the picker fills the pane and centres itself in it", async ({ page }) => {
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#panePicker")).toBeVisible();

  const pane = (await page.locator("#sidePane").boundingBox())!;
  const picker = (await page.locator("#panePicker").boundingBox())!;
  // It is the whole empty state, not a strip under a body that took the room.
  expect(picker.height).toBeGreaterThan(pane.height * 0.8);
  // And the rows sit around the middle of it rather than at the bottom.
  const rowsCentre = await page.evaluate(() => {
    const rows = document.querySelectorAll("#panePicker .pane-picker-row");
    const first = rows[0].getBoundingClientRect();
    const last = rows[rows.length - 1].getBoundingClientRect();
    return Math.round((first.top + last.bottom) / 2);
  });
  const paneMid = Math.round(pane.y + pane.height / 2);
  expect(Math.abs(rowsCentre - paneMid)).toBeLessThan(pane.height * 0.25);
  // The body has nothing to show, so it must not take half the column.
  await expect(page.locator("#paneBody")).toBeHidden();
});

test("Agent mode comes back from the IDE at full width when the pane is empty", async ({ page }) => {
  await start(page);
  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible({ timeout: 15_000 });

  await page.locator("#modeAgentBtn").click();
  await expect(page.locator("#agentView")).toBeVisible();
  // Nothing to show in the pane, so the conversation gets the window. This used
  // to reopen at half width with an empty column beside the chat.
  await expect(page.locator("#sidePane")).toBeHidden();
  await expect(page.locator("#panePicker")).toBeHidden();
});

test("Agent mode keeps the pane when it does have something to show", async ({ page }) => {
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await page.locator('#panePicker [data-pane-pick="chat"]').click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();

  await page.locator("#ideModeBtn").click();
  await expect(page.locator("#ideWorkspace")).toBeVisible({ timeout: 15_000 });
  await page.locator("#modeAgentBtn").click();
  // The side chat is still there, so the pane is too — that is the one case the
  // pane should not take away from you.
  await expect(page.locator("#sidePane")).toBeVisible();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
});

test("the header + opens the pane without a dropdown over it", async ({ page }) => {
  await start(page);
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  await expect(page.locator("#panePicker")).toBeVisible();
  // No little "New side chat / New terminal" menu: the picker already says the
  // same three things, in the pane, without covering the conversation.
  await expect(page.locator("#paneHistoryMenu")).toBeHidden();
  await expect(page.getByRole("menuitem")).toHaveCount(0);

  // Pressing it again leaves the pane where it is; closing is the pane's ✕.
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane")).toBeVisible();
});

test("the window controls sit on the same line as the header icons", async ({ page }) => {
  await start(page);
  const plus = await centreY(page, "#paneNewChatBtn");
  const moon = await centreY(page, "#themeToggleBtn");
  const min = await centreY(page, "#windowMinimizeBtn");
  const max = await centreY(page, "#windowMaximizeBtn");
  const close = await centreY(page, "#windowCloseBtn");

  // All five on one centre line: the controls are the window's, the icons the
  // app's, but they share the bar and a few pixels of drift is visible.
  for (const [name, value] of [
    ["minimise", min],
    ["maximise", max],
    ["close", close],
  ] as const) {
    expect(Math.abs(value - plus), `${name} vs the header +`).toBeLessThanOrEqual(1);
    expect(Math.abs(value - moon), `${name} vs the moon`).toBeLessThanOrEqual(1);
  }
});
