// tabstrip.spec.ts pins the pane's tab strip. The full-width, vertical "Open
// tabs" drawer belonged to the old tabs layout and is gone: browser pages get
// their own floating list inside Browser mode, and the pane keeps one
// horizontal strip of the things it owns — side chats, terminals and browser.
import { expect, test, type Page } from "@playwright/test";

import { showPane, addToPane } from "./modes";

async function start(page: Page): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("hello");
}

async function openSideChat(page: Page): Promise<void> {
  await showPane(page);
  await addToPane(page, "chat");
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
}

test("the pane's strip is horizontal and lists what the pane owns", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  await openSideChat(page);

  const tabs = page.locator("#paneTabs");
  expect(await tabs.evaluate((el) => getComputedStyle(el).flexDirection)).toBe("row");
  await expect(page.locator("#paneTabs .pane-tab", { hasText: "Side chat" })).toHaveCount(2);
  // The agent is the conversation beside the pane, never a tab in it.
  await expect(page.locator('#paneTabs .pane-tab[data-key="main"]')).toHaveCount(0);
  await expect(page.locator("#agentMain")).toBeVisible();
});

test("there is no vertical tab list anywhere", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  // Every vertical list is gone, in the shell and in the browser.
  await expect(page.locator("#paneTabsDrawer")).toHaveCount(0);
  await expect(page.locator("#paneDrawerTabs")).toHaveCount(0);
  await expect(page.locator(".pane-strip.is-vertical")).toHaveCount(0);
  await expect(page.locator("#browserDrawer")).toHaveCount(0);
  await expect(page.locator("#browserDockBtn")).toHaveCount(0);
  await expect(page.locator(".browser-drawer")).toHaveCount(0);
  await expect(page.locator("#browserBar [aria-label='Show all tabs']")).toHaveCount(0);
  await expect(page.locator("#paneRailToggle")).toBeHidden();
});

test("the strip keeps every tab when the pane is closed and reopened", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  await openSideChat(page);
  const count = await page.locator("#paneTabs .pane-tab").count();
  expect(count).toBe(2);

  await page.locator("#paneCloseBtn").click();
  await expect(page.locator("#sidePane")).toBeHidden();
  await page.locator("#paneNewChatBtn").click();
  await page.keyboard.press("Escape");
  // Tabs are the pane's content, not its open state: closing the pane keeps
  // them, which is what makes reopening it instant.
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(count);
});

test("picking a tab makes it active and a close button removes just that one", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await start(page);
  await openSideChat(page);
  await openSideChat(page);
  const strip = page.locator("#paneTabs .pane-tab");
  await expect(strip.nth(1)).toHaveClass(/is-active/);

  await strip.nth(0).locator(".pane-tab-pick").click();
  await expect(strip.nth(0)).toHaveClass(/is-active/);

  await strip.nth(1).locator(".pane-tab-close").click();
  await expect(page.locator("#paneTabs .pane-tab")).toHaveCount(1);
  await expect(page.locator("#sidePane")).toBeVisible();
});
