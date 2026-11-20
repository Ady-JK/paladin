// tabstrip.spec.ts pins the navigation in each layout.
//  - Split: the Agent is always the left half, so the pane's horizontal strip
//    lists only the right-hand tabs. The rail's browser button opens the
//    "Open tabs" drawer (Agent first) in either layout.
//  - Tabs: the drawer is the only navigation, vertical, Agent at the top.
//
// The app opens in tabs layout, so every split test asks for split first.
import { expect, test, type Page } from "@playwright/test";

import { useLayout } from "./layout";

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

test("split view keeps a horizontal strip of right-side tabs, without Agent", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await useLayout(page, "split");
  await openSideChat(page);
  const strip = page.locator("#sidePane .pane-strip");
  const tabs = page.locator("#paneTabs");
  // Horizontal: tabs flow left to right, so the strip is wide and short.
  expect(await tabs.evaluate((el) => getComputedStyle(el).flexDirection)).toBe("row");
  // The agent is the left half; it is not a tab on the right.
  await expect(page.locator('#paneTabs .pane-tab[data-key="main"]')).toHaveCount(0);
  await expect(page.locator("#paneTabs .pane-tab", { hasText: "Side chat" })).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
  const stripBox = (await strip.boundingBox())!;
  const firstTab = (await page.locator("#paneTabs .pane-tab").first().boundingBox())!;
  expect(stripBox.width).toBeGreaterThan(firstTab.width);
});

test("the browser button is always on the rail and its drawer lists Agent first in split view", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await useLayout(page, "split");
  await openSideChat(page);
  const railBtn = page.locator("#paneRailToggle");
  const drawer = page.locator("#paneTabsDrawer");
  await expect(railBtn).toBeVisible();
  await expect(page.locator("#agentView")).not.toHaveClass(/is-tab-layout/);
  // Split starts with the drawer closed: the strip already shows the tabs.
  await expect(drawer).toBeHidden();
  await railBtn.click();
  await expect(drawer).toBeVisible();
  const list = page.locator("#paneDrawerTabs");
  expect(await list.evaluate((el) => getComputedStyle(el).flexDirection)).toBe("column");
  await expect(list.locator(".pane-tab").first()).toHaveAttribute("data-key", "main");
  await expect(list.locator(".pane-tab", { hasText: "Side chat" })).toBeVisible();
  // The pane and its strip stay beside the agent while the drawer is open.
  await expect(page.locator("#paneTabs")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();

  // Picking Agent in split keeps the pane: the agent is already on screen.
  await list.locator('.pane-tab[data-key="main"] .pane-tab-pick').click();
  await expect(page.locator("#sidePane")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#promptInput")).toBeFocused();

  await railBtn.click();
  await expect(drawer).toBeHidden();
});

test("picking a tab in the split drawer selects it in the right pane", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await useLayout(page, "split");
  await openSideChat(page);
  await openSideChat(page);
  const strip = page.locator("#paneTabs .pane-tab");
  await expect(strip).toHaveCount(2);
  // The newest tab is active; the drawer (Agent first) selects the other one.
  await expect(strip.nth(1)).toHaveClass(/is-active/);
  await page.locator("#paneRailToggle").click();
  await page.locator("#paneDrawerTabs .pane-tab").nth(1).locator(".pane-tab-pick").click();
  await expect(strip.nth(0)).toHaveClass(/is-active/);
  await expect(page.locator("#agentMain")).toBeVisible();
});

test("tab view navigates through the drawer, Agent at the top", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  await useLayout(page, "tabs");
  const tabs = page.locator("#paneDrawerTabs");
  await expect.poll(() => tabs.evaluate((el) => getComputedStyle(el).flexDirection)).toBe("column");
  await expect(tabs.locator(".pane-tab").first()).toHaveAttribute("data-key", "main");
  // The list is the "Open tabs" drawer, a column of the shell beside the
  // card, and the pane itself has no strip.
  const drawer = page.locator("#paneTabsDrawer");
  await expect(drawer).toBeVisible();
  await expect(drawer.locator(".pane-strip")).toBeVisible();
  await expect(page.locator("#sidePane .pane-strip")).toBeHidden();
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
  await useLayout(page, "tabs");
  const railBtn = page.locator("#paneRailToggle");
  await expect(railBtn).toBeVisible();
  await expect(railBtn).toHaveAttribute("aria-pressed", "true");
  // Tabs layout opens its drawer with a slide; measure once it has settled.
  await expect(page.locator("#paneTabsDrawer")).toBeVisible();
  await page.waitForTimeout(500);
  const open = (await page.locator("#sidePane").boundingBox())!.width;

  await railBtn.click();
  await expect(railBtn).toHaveAttribute("aria-pressed", "false");
  await expect(page.locator("#paneTabsDrawer")).toBeHidden();
  await expect.poll(async () => (await page.locator("#sidePane").boundingBox())!.width).toBeGreaterThan(open + 100);

  await railBtn.click();
  await expect(page.locator("#paneTabsDrawer")).toBeVisible();
});

test("picking the Agent entry shows the conversation and keeps the list", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  await useLayout(page, "tabs");
  const before = await page.locator("#paneDrawerTabs .pane-tab").count();
  expect(before).toBeGreaterThan(1);

  await page.locator('#paneDrawerTabs .pane-tab[data-key="main"] .pane-tab-pick').click();
  // A tab selects; it does not close the list behind it.
  await expect(page.locator("#messages")).toBeVisible();
  await expect(page.locator("#paneDrawerTabs .pane-tab")).toHaveCount(before);
  await expect(page.locator('#paneDrawerTabs .pane-tab[data-key="main"]')).toHaveClass(/is-active/);

  // And the other tab is still selectable.
  await page.locator("#paneDrawerTabs .pane-tab", { hasText: "Side chat" }).locator(".pane-tab-pick").click();
  await expect(page.locator("#messages")).toBeHidden();
});

test("split view always has its right pane, empty or not", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await useLayout(page, "split");
  const pane = page.locator("#sidePane");
  // Nothing is open yet, but the pane is there and says so.
  await expect(pane).toBeVisible();
  await expect(pane).toContainText("Nothing open");
  await expect(page.locator("#paneCloseBtn")).toBeHidden();

  await openSideChat(page);
  await expect(pane).not.toContainText("Nothing open");
  await pane.locator(".pane-tab-close").first().click();
  // Closing the last tab leaves the pane and does not respawn a side chat.
  await expect(pane).toBeVisible();
  await expect(pane).toContainText("Nothing open");
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(0);
  await expect(page.locator("#agentMain")).toBeVisible();

  // Agent-only width is a layout choice: tabs layout hides the pane behind the drawer.
  await useLayout(page, "tabs");
  await expect(pane).toBeHidden();
  await expect(page.locator("#agentMain")).toBeVisible();
});
