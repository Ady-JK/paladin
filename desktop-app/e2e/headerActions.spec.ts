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

test("the header carries only the icon row; readings live in the composer", async ({ page }) => {
  await expect(page.locator("#headerMetrics")).toHaveCount(0);
  await expect(page.locator("#titleBar #usagePill")).toHaveCount(0);
  await expect(page.locator("#titleBar #resPill")).toHaveCount(0);
  // The donut sits in the composer toolbar, and changing its text never
  // moves the header's icon row.
  await expect(page.locator(".composer-toolbar #usagePill")).toBeVisible();
  const actions = page.locator("#paneActions");
  const before = (await actions.boundingBox())!.x;
  await page.locator("#usageLabel").evaluate((el) => {
    el.textContent = "100% · $1234.5678 · 120k tok";
  });
  expect(Math.abs((await actions.boundingBox())!.x - before)).toBeLessThan(1);
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

test("+, theme, minimize, maximize and close sit on one even pitch", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  const selectors = [
    "#paneNewChatBtn",
    "#themeToggleBtn",
    "#windowMinimizeBtn",
    "#windowMaximizeBtn",
    "#windowCloseBtn",
  ];
  const centers: number[] = [];
  for (const selector of selectors) {
    const box = (await page.locator(selector).boundingBox())!;
    centers.push(box.x + box.width / 2);
  }
  const gaps = centers.slice(1).map((x, i) => Math.round(x - centers[i]));
  expect(gaps, `centres ${centers.join(", ")}`).toEqual([40, 40, 40, 40]);
});
