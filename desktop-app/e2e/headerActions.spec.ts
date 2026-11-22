import { expect, test } from "@playwright/test";
import { hidePane, showPane } from "./modes";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
});

test("the header + opens the pane, whose picker offers a side chat", async ({ page }) => {
  const actions = page.locator("#titleBar #paneActions");
  await expect(actions).toBeVisible();
  // The layout toggle that used to sit here is gone: the rail's mode buttons
  // replaced it, so the header keeps exactly one pane control.
  await expect(actions.locator("#paneLayoutBtn")).toHaveCount(0);
  await actions.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  // + opens the pane and nothing else: no dropdown of "New side chat / New
  // terminal" to compete with the picker that already offers both.
  await expect(page.locator("#paneHistoryMenu")).toBeHidden();
  await page.locator('#panePicker [data-pane-pick="chat"]').click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  await hidePane(page);
  // A tab is the pane's content, not its open state.
  await showPane(page);
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
});

test("the readings sit left of the icon row and never push it sideways", async ({ page }) => {
  const actions = page.locator("#paneActions");
  const metrics = page.locator("#headerMetrics");
  await expect(metrics).toBeVisible();
  await expect(page.locator("#headerMetrics #usagePill")).toBeVisible();

  // Readings left, action icons right: a growing pill extends into the
  // free space instead of shifting the IDE/Creator/layout icons.
  const read = async (): Promise<{ metrics: number; actions: number }> => ({
    metrics: (await metrics.boundingBox())!.x,
    actions: (await actions.boundingBox())!.x,
  });
  const before = await read();
  expect(before.metrics).toBeLessThan(before.actions);

  await page.locator("#headerMetrics #usageLabel").evaluate((el) => {
    el.textContent = "100% · $1234.5678 · 120k tok";
  });
  const after = await read();
  expect(after.metrics).toBeGreaterThanOrEqual(before.metrics);
  expect(Math.abs(after.actions - before.actions)).toBeLessThan(1);
});

test("browser has one entry, on the rail, and the + picker offers every pane kind", async ({ page }) => {
  await expect(page.locator("#paneBrowserBtn")).toHaveCount(0);
  // Exactly one Browser control, and it is the rail's mode button.
  await expect(page.getByRole("tab", { name: "Browser", exact: true })).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Browser", exact: true })).toHaveCount(0);
  await page.locator("#paneNewChatBtn").click();
  const picker = page.locator("#panePicker");
  await expect(picker).toBeVisible();
  await expect(picker.getByRole("button", { name: /Side chat/ })).toBeVisible();
  await expect(picker.getByRole("button", { name: /Terminal/ })).toBeVisible();
  await expect(picker.getByRole("button", { name: /Browser/ })).toBeVisible();
  await picker.getByRole("button", { name: /Terminal/ }).click();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
});

// Switching modes must not resize the header: the bar belongs to Agent mode
// only, and opening or closing the pane used to be able to grow it. The window
// controls are pinned outside the header, so they are measured for "does not
// move", not for "gives the header its height".
test("the header keeps one height as the pane opens and the mode changes", async ({ page }) => {
  const bar = page.locator("#titleBar");
  const measure = async (): Promise<{ bar: number; actions: number; close: number; closeY: number }> => ({
    bar: Math.round((await bar.boundingBox())!.height),
    actions: Math.round((await page.locator("#headerStatusActions").boundingBox())!.height),
    close: Math.round((await page.locator("#windowCloseBtn").boundingBox())!.height),
    closeY: Math.round((await page.locator("#windowCloseBtn").boundingBox())!.y),
  });
  const closed = await measure();
  await showPane(page);
  await page.locator('#panePicker [data-pane-pick="chat"]').click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  const withPane = await measure();

  expect(withPane.bar).toBe(closed.bar);
  expect(withPane.actions).toBe(closed.actions);
  expect(withPane.close).toBe(closed.close);
  expect(withPane.closeY).toBe(closed.closeY);

  // Browser mode has no agent header at all, so nothing to measure there.
  await page.locator("#modeBrowserBtn").click();
  await expect(bar).toBeHidden();
  await page.locator("#modeAgentBtn").click();
  await expect(bar).toBeVisible();
  expect((await measure()).bar).toBe(closed.bar);
});
