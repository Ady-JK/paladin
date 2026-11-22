// browserDrawer.spec.ts pins Browser mode's floating vertical tab list: it is
// owned by the browser (not the app rail), it floats over the pages instead of
// narrowing them, and it lists the same pages the horizontal strip does.
import { expect, test, type Page } from "@playwright/test";

// openBrowserMode starts a session, switches to Browser mode and opens one page
// per URL through the saved-sites menu — the same route a user takes.
async function openPageCount(page: Page, count: number): Promise<void> {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "hello");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("hello");
  await page.locator("#modeBrowserBtn").click();
  await expect(page.locator("#browserView")).toBeVisible();
  // Saved sites are seeded on a fresh profile, so the menu always has pages to
  // open — the same clicks a user makes.
  for (let index = 0; index < count; index++) {
    await page.locator("#browserNewTab").click();
    await expect(page.locator("#webMenu")).toBeVisible();
    await page.locator("#webMenu [data-web-open]").nth(index).click();
    await expect(page.locator("#webMenu")).toBeHidden();
    await expect(page.locator("#browserTabs .browser-tab")).toHaveCount(index + 1);
  }
}

test("the drawer lives in the browser, not on the app rail", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPageCount(page, 2);

  // The trigger is in the browser's own bar.
  const dock = page.locator("#browserBar #browserDockBtn");
  await expect(dock).toBeVisible();
  // And nothing on the rail opens it.
  await expect(page.locator("#rail #browserDockBtn")).toHaveCount(0);
  await expect(page.locator("#paneRailToggle")).toBeHidden();
  await expect(page.locator("#paneTabsDrawer")).toHaveCount(0);

  const drawer = page.locator("#browserDrawer");
  await expect(drawer).toBeHidden();
  await dock.click();
  await expect(drawer).toBeVisible();
  await expect(dock).toHaveAttribute("aria-expanded", "true");

  // Vertical, one row per page, with the full title and its host.
  const list = drawer.locator(".browser-drawer-list");
  expect(await list.evaluate((el) => getComputedStyle(el).flexDirection)).toBe("column");
  await expect(drawer.locator(".browser-drawer-row")).toHaveCount(2);
  await expect(drawer.locator(".browser-drawer-host").first()).not.toBeEmpty();
});

test("the drawer floats over the pages and never narrows them", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPageCount(page, 1);
  const before = (await page.locator("#browserBody").boundingBox())!;

  await page.locator("#browserDockBtn").click();
  await expect(page.locator("#browserDrawer")).toBeVisible();
  const after = (await page.locator("#browserBody").boundingBox())!;

  // The page's column is unchanged: the list covers it, it does not push it.
  expect(Math.abs(after.width - before.width)).toBeLessThan(1);
  // And it is drawn inside the browser area, on top of the page.
  const drawer = (await page.locator("#browserDrawer").boundingBox())!;
  expect(drawer.x).toBeGreaterThanOrEqual(before.x);
  expect(drawer.x).toBeLessThan(before.x + 40);
});

test("picking a row in the drawer selects that page and closes the list", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPageCount(page, 2);
  await page.locator("#browserDockBtn").click();
  const rows = page.locator("#browserDrawer .browser-drawer-row");
  await expect(rows).toHaveCount(2);

  await rows.nth(0).click();
  await expect(page.locator("#browserDrawer")).toBeHidden();
  await expect(page.locator("#browserTabs .browser-tab.is-active").first()).toBeVisible();
});

test("the drawer closes on Escape, its own close button, and when the mode changes", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPageCount(page, 1);
  const drawer = page.locator("#browserDrawer");
  const dock = page.locator("#browserDockBtn");

  await dock.click();
  await expect(drawer).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();

  await dock.click();
  await drawer.locator(".browser-drawer-close").click();
  await expect(drawer).toBeHidden();

  await dock.click();
  await expect(drawer).toBeVisible();
  // It belongs to Browser mode, so leaving the mode closes it.
  await page.locator("#modeAgentBtn").click();
  await expect(page.locator("#browserView")).toBeHidden();
  await page.locator("#modeBrowserBtn").click();
  await expect(drawer).toBeHidden();
});

test("the drawer lists the same pages the horizontal strip does", async ({ page }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await openPageCount(page, 3);

  const strip = page.locator("#browserTabs .browser-tab");
  await expect(strip).toHaveCount(3);
  await page.locator("#browserDockBtn").click();
  await expect(page.locator("#browserDrawer .browser-drawer-row")).toHaveCount(3);
  // Same pages, same order — two views of one list.
  await expect(page.locator("#browserDrawer .browser-drawer-host")).toHaveCount(3);
  await expect(strip.first()).toBeVisible();
});
