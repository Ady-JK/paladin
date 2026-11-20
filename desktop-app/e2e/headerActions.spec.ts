import { expect, test } from "@playwright/test";
import { useLayout } from "./layout";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
});

test("header menu opens side chats and preserves layout controls", async ({ page }) => {
  await useLayout(page, "split");
  const actions = page.locator("#titleBar #paneActions");
  await expect(actions).toBeVisible();
  await expect(actions.locator("#paneLayoutBtn")).toBeVisible();
  await actions.locator("#paneNewChatBtn").click();
  await page.getByRole("menuitem", { name: "New side chat" }).click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  await useLayout(page, "tabs");
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

test("browser has one entry and new side chats and terminals share a menu", async ({ page }) => {
  await expect(page.locator("#paneBrowserBtn")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Browser", exact: true })).toHaveCount(1);
  await page.locator("#paneNewChatBtn").click();
  await expect(page.getByRole("menuitem", { name: "New side chat" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "New terminal" })).toBeVisible();
  await expect(page.locator("#paneHistoryMenu")).toContainText("No saved side chats");
  await page.getByRole("menuitem", { name: "New terminal" }).click();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
});

// Switching layouts must not resize the header: the tab strip moves into
// the title bar in tabs layout, and a taller strip used to grow the bar
// (and with it the readings row and the window buttons) by six pixels.
test("the header keeps one height across layouts", async ({ page }) => {
  await useLayout(page, "split");
  const bar = page.locator("#titleBar");
  const measure = async (): Promise<{ bar: number; actions: number; close: number; closeY: number }> => ({
    bar: Math.round((await bar.boundingBox())!.height),
    actions: Math.round((await page.locator("#headerStatusActions").boundingBox())!.height),
    close: Math.round((await page.locator("#windowCloseBtn").boundingBox())!.height),
    closeY: Math.round((await page.locator("#windowCloseBtn").boundingBox())!.y),
  });
  const split = await measure();
  await page.locator("#paneActions #paneNewChatBtn").click();
  await page.getByRole("menuitem", { name: "New side chat" }).click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  const splitWithPane = await measure();
  await useLayout(page, "tabs");
  // The tab list is its own shell drawer now, not part of the header and
  // not a column inside the split, so the bar's height must not depend on
  // the tabs at all.
  await expect(page.locator("#titleBar .pane-strip")).toHaveCount(0);
  await expect(page.locator("#agentSplit > .pane-strip")).toHaveCount(0);
  await expect(page.locator("#paneTabsDrawer .pane-strip")).toBeVisible();
  const tabs = await measure();

  expect(tabs.bar, "the bar must not grow in tabs layout").toBe(split.bar);
  expect(splitWithPane.bar).toBe(split.bar);
  expect(tabs.actions).toBe(split.actions);
  expect(tabs.close).toBe(split.close);
  expect(tabs.closeY).toBe(split.closeY);
});
