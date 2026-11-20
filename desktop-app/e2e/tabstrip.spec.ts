// tabstrip.spec.ts pins the strip's shape in each layout: horizontal in
// split view, vertical in tab view, with the Agent tab pinned at position
// zero and never destroying the strip.
import { expect, test, type Page } from "@playwright/test";

async function start(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("hello");
}

async function openSideChat(page: Page): Promise<void> {
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await expect(page.locator("#sidePane")).toBeVisible();
}

test("split view keeps a horizontal strip with Agent first", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  const strip = page.locator(".pane-strip");
  const tabs = page.locator("#paneTabs");
  // Horizontal: tabs flow left to right, so the strip is wide and short.
  const direction = await tabs.evaluate((el) => getComputedStyle(el).flexDirection);
  expect(direction).toBe("row");
  // Agent is tab zero in both layouts.
  await expect(page.locator("#paneTabs .pane-tab").first()).toHaveAttribute("data-key", "main");
  const stripBox = (await strip.boundingBox())!;
  const firstTab = (await page.locator("#paneTabs .pane-tab").first().boundingBox())!;
  // It sits inside the pane, at the top of it.
  expect(stripBox.width).toBeGreaterThan(firstTab.width);
});

test("tab view moves the tab list into its own drawer", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  await page.locator("#paneLayoutBtn").click();
  const tabs = page.locator("#paneTabs");
  await expect.poll(() => tabs.evaluate((el) => getComputedStyle(el).flexDirection)).toBe("column");
  await expect(tabs.locator(".pane-tab").first()).toHaveAttribute("data-key", "main");
  // The list is the "Open tabs" drawer, a column of the shell beside the
  // card, not a column inside the split.
  const drawer = page.locator("#paneTabsDrawer");
  await expect(drawer).toBeVisible();
  await expect(drawer.locator(".pane-strip")).toBeVisible();
  const bounds = (await drawer.boundingBox())!;
  const card = (await page.locator("#mainCard").boundingBox())!;
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(card.x + 1.5);
  // Full titles instead of truncated ones.
  await expect(tabs.locator(".pane-tab", { hasText: "Side chat" })).toBeVisible();
});

test("the tabs drawer opens from the rail and gives the page the whole window", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  const railBtn = page.locator("#paneRailToggle");
  // Split view has no tab list to show, so the button is not offered.
  await expect(railBtn).toBeHidden();
  await page.locator("#paneLayoutBtn").click();
  await expect(railBtn).toBeVisible();
  await expect(railBtn).toHaveAttribute("aria-pressed", "true");
  const open = (await page.locator("#sidePane").boundingBox())!.width;

  await railBtn.click();
  await expect(railBtn).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator("#paneTabsDrawer")).toBeHidden();
  await expect.poll(async () => (await page.locator("#sidePane").boundingBox())!.width).toBeGreaterThan(open + 100);

  await railBtn.click();
  await expect(page.locator("#paneTabsDrawer")).toBeVisible();
});

test("picking the Agent tab shows the conversation and keeps the strip", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  await page.locator("#paneLayoutBtn").click();
  const before = await page.locator("#paneTabs .pane-tab").count();
  expect(before).toBeGreaterThan(1);

  await page.locator('#paneTabs .pane-tab[data-key="main"] .pane-tab-pick').click();
  // A tab selects; it does not close the strip behind it.
  await expect(page.locator("#messages")).toBeVisible();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(before);
  await expect(page.locator('#paneTabs .pane-tab[data-key="main"]')).toHaveClass(/is-active/);

  // And the other tab is still selectable.
  await page.locator("#paneTabs .pane-tab", { hasText: "Side chat" }).locator(".pane-tab-pick").click();
  await expect(page.locator("#messages")).toBeHidden();
});

test("closing the pane in split view keeps the tabs", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  const tabs = page.locator("#paneTabs .pane-tab");
  const count = await tabs.count();
  expect(count).toBeGreaterThan(1);
  const keys = await tabs.evaluateAll((els) => els.map((el) => (el as HTMLElement).dataset.key));

  await page.locator("#paneCloseBtn").click();
  await expect(page.locator("#sidePane")).toBeHidden();
  // The strip lives inside the pane, so it retracts with it. What must not
  // happen is the tabs being thrown away: reopening shows them again.
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  const after = await tabs.evaluateAll((els) => els.map((el) => (el as HTMLElement).dataset.key));
  // Every tab that was there is still there, plus the one just opened.
  for (const key of keys) expect(after).toContain(key);
  expect(after.length).toBe(count + 1);
});
