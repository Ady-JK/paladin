import { expect, test, type Page } from "@playwright/test";

async function switchLayout(page: Page): Promise<void> {
  await page.locator("#paneHistoryBtn").click();
  await page.locator("#paneLayoutBtn").click();
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.fill("#promptInput", "start");
  await page.press("#promptInput", "Enter");
  await expect(page.locator("#messages")).toContainText("start");
});

test("split and tabs preserve the active side chat and make each view full width", async ({ page }) => {
  const layout = page.locator("#paneLayoutBtn");
  await expect(page.locator("#paneHistoryBtn")).toBeVisible();
  await expect(layout).toBeHidden();
  await expect(layout).toHaveAttribute("data-layout", "split");
  await page.locator("#sidePaneBtn").click();
  const splitWidth = (await page.locator("#sidePane").boundingBox())!.width;
  const fullWidth = (await page.locator("#agentSplit").boundingBox())!.width;
  expect(splitWidth).toBeLessThan(fullWidth * 0.7);

  await switchLayout(page);
  await expect(page.locator("#agentView")).toHaveClass(/is-tab-layout/);
  await expect(page.locator("#titleBar .pane-strip")).toBeVisible();
  await expect(page.locator("#sidePane .pane-strip")).toHaveCount(0);
  await expect(layout.locator("svg")).toHaveCount(0);
  await expect(layout).toHaveText("Split view");
  await expect(page.locator("#agentMain")).toBeHidden();
  expect((await page.locator("#sidePane").boundingBox())!.width).toBeGreaterThan(fullWidth * 0.9);

  await page.locator('[data-key="main"] .pane-tab-pick').click();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#sidePane .pane-body")).toBeHidden();
  await page.locator('#paneTabs .pane-tab[data-key^="t"] .pane-tab-pick').first().click();
  await expect(page.locator("#sidePane .side-chat")).toBeVisible();

  await switchLayout(page);
  await expect(page.locator("#sidePane .pane-strip")).toBeVisible();
  await expect(page.locator("#agentMain")).toBeVisible();
  await expect(page.locator("#sidePane .side-chat")).toBeVisible();
});

test("clicking a localhost chat link opens the session browser in either layout", async ({ page }) => {
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

  await switchLayout(page);
  await page.locator('[data-key="main"] .pane-tab-pick').click();
  await page.locator('#messages a[data-ext="http://localhost:3000"]').click();
  await expect(page.locator("#agentMain")).toBeHidden();
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
  await expect(page.locator("#paneTabs [data-browser]")).toHaveCount(1);
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserOpens?.length ?? 0)).toBe(2);
});

test("browser tab expands to full width and the layout survives a reload", async ({ page }) => {
  await page.locator("#sidePaneBtn").click();
  await page.locator("#paneBrowserBtn").click();
  await page.locator("#sidePane .browser-url").fill("http://127.0.0.1:3000");
  await page.locator("#sidePane .browser-open").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserBounds?.width ?? 0)).toBeGreaterThan(100);
  const splitWidth = await page.evaluate(() => (window as any).__e2eBrowserBounds.width);
  await switchLayout(page);
  await expect
    .poll(() => page.evaluate(() => (window as any).__e2eBrowserBounds?.width ?? 0))
    .toBeGreaterThan(splitWidth * 1.5);
  await page.locator('[data-key="main"] .pane-tab-pick').click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserVisible)).toBe(null);
  await page.locator("#paneTabs [data-browser] .pane-tab-pick").click();
  await expect.poll(() => page.evaluate(() => (window as any).__e2eBrowserVisible)).toBeTruthy();

  await page.reload();
  await expect(page.locator("#agentView")).toHaveClass(/is-tab-layout/);
  await expect(page.locator("#paneLayoutBtn")).toHaveAttribute("data-layout", "tabs");
});

test("agent tabs stay out of the external Chat header", async ({ page }) => {
  await switchLayout(page);
  await expect(page.locator("#titleBar .pane-strip")).toBeVisible();
  await page.locator("#chatTab").click();
  await expect(page.locator("#titleBar .pane-strip")).toBeHidden();
  await expect(page.locator("#headerIdentity")).toBeVisible();
  await page.locator("#agentTab").click();
  await expect(page.locator("#titleBar .pane-strip")).toBeVisible();
});

test("saved side chat menu stays usable in the unified header", async ({ page }) => {
  await switchLayout(page);
  await page.locator("#paneHistoryBtn").click();
  await expect(page.locator("#paneHistoryMenu")).toBeVisible();
  await expect(page.locator("#paneHistoryMenu")).toContainText("No saved side chats");
});
