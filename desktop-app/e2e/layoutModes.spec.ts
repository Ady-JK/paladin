// layoutModes.spec.ts pins the two layouts and what survives a switch.
// The app opens in tabs layout, so a test that describes split asks for it.
import { expect, test } from "@playwright/test";

import { useLayout } from "./layout";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "start");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("start");
});

test("a fresh start opens in the tabs layout", async ({ page }) => {
  const layout = page.locator("#paneLayoutBtn");
  await expect(layout).toHaveAttribute("data-layout", "tabs");
  await expect(page.locator("#agentView")).toHaveClass(/is-tab-layout/);
  // The conversation is the whole window; the pane waits behind the drawer.
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#sidePane")).toBeHidden();
  await expect(page.locator("#paneTabsDrawer")).toBeVisible();
});

test("split and tabs preserve the active side chat and make each view full width", async ({ page }) => {
  const layout = page.locator("#paneLayoutBtn");
  await useLayout(page, "split");
  await expect(page.locator("#paneNewChatBtn")).toBeVisible();
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  const splitWidth = (await page.locator("#sidePane").boundingBox())!.width;
  const fullWidth = (await page.locator("#agentSplit").boundingBox())!.width;
  expect(splitWidth).toBeLessThan(fullWidth * 0.7);

  await useLayout(page, "tabs");
  // Tab view: the tab list is the "Open tabs" drawer beside the card, not
  // part of the header and no longer a column inside the split, so the body
  // gets the whole split.
  await expect(page.locator("#paneTabsDrawer")).toBeVisible();
  await expect(page.locator("#titleBar .pane-strip")).toHaveCount(0);
  // The pane carries no strip in tabs layout: the drawer is the navigation.
  await expect(page.locator("#sidePane .pane-strip")).toBeHidden();
  await expect(page.locator("#agentSplit > .pane-strip")).toHaveCount(0);
  await expect(layout).toHaveAttribute("aria-label", "Switch to split layout");
  await expect(page.locator("#agentMain")).toBeHidden();
  // The body fills the whole split: the tab list moved out to the drawer,
  // which is a shell column and narrows the card instead of the split.
  const body = (await page.locator("#sidePane").boundingBox())!.width;
  const split = (await page.locator("#agentSplit").boundingBox())!.width;
  expect(body).toBeGreaterThanOrEqual(split - 2);

  await page.locator('[data-key="main"] .pane-tab-pick').click();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#sidePane .pane-body")).toBeHidden();
  await page.locator('#paneDrawerTabs .pane-tab[data-key^="t"] .pane-tab-pick').first().click();
  await expect(page.locator("#sidePane .side-chat")).toBeVisible();

  await useLayout(page, "split");
  await expect(page.locator("#sidePane .pane-strip")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#sidePane .side-chat")).toBeVisible();
});

test("clicking a localhost chat link opens the session browser in either layout", async ({ page }) => {
  await useLayout(page, "split");
  await page.locator("#messages").evaluate((el) => {
    el.insertAdjacentHTML(
      "beforeend",
      '<a class="md-link" href="#" data-ext="http://localhost:3000">http://localhost:3000</a>',
    );
  });
  await page.locator('#messages a[data-ext="http://localhost:3000"]').click();
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
  await expect(page.locator("#sidePane .browser-url")).toHaveValue("http://localhost:3000");
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserOpens?.length ?? 0)).toBe(1);

  await useLayout(page, "tabs");
  await page.locator('[data-key="main"] .pane-tab-pick').click();
  await page.locator('#messages a[data-ext="http://localhost:3000"]').click();
  await expect(page.locator("#agentMain")).toBeHidden();
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
  await expect(page.locator("#paneDrawerTabs [data-browser]")).toHaveCount(1);
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserOpens?.length ?? 0)).toBe(2);
});

test("browser tab expands to full width and the layout survives a reload", async ({ page }) => {
  await useLayout(page, "split");
  await page.locator("#paneNewChatBtn").click();
  await page.locator("[data-pane-new-chat]").click();
  await page.evaluate(() =>
    (window as any).__e2eHub.emit({
      event: "browser/opened",
      sessionId: (window as any).__e2eSubmits[0].sessionId,
      url: "http://localhost:8080",
    }),
  );
  await page.locator("#sidePane .browser-url").fill("http://127.0.0.1:3000");
  await page.locator("#sidePane .browser-open").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserBounds?.width ?? 0)).toBeGreaterThan(100);
  const splitWidth = await page.evaluate(() => (window as any).__e2eBrowserBounds.width);
  await useLayout(page, "tabs");
  // Tab view fills the split, minus the tabs drawer when it is open — not
  // "1.5x the split", which stops being true once the drawer has width.
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eBrowserBounds?.width ?? 0))
    .toBeGreaterThan(splitWidth + 100);
  await page.locator('[data-key="main"] .pane-tab-pick').click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserVisible)).toBe(null);
  await page.locator("#paneDrawerTabs [data-browser] .pane-tab-pick").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserVisible)).toBeTruthy();

  await page.reload();
  await expect(page.locator("#agentView")).toHaveClass(/is-tab-layout/);
  await expect(page.locator("#paneLayoutBtn")).toHaveAttribute("data-layout", "tabs");
});

test("saved side chat menu stays usable in the unified header", async ({ page }) => {
  await useLayout(page, "tabs");
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#paneHistoryMenu")).toBeVisible();
  await expect(page.locator("#paneHistoryMenu")).toContainText("No saved side chats");
});
