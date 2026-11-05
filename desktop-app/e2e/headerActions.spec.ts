import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  await page.addInitScript({ path: "e2e/paladin-stub.js" });
  await page.goto("/");
  await page.locator("#promptInput").fill("start");
  await page.locator("#promptInput").press("Enter");
});

test("shared Agent header actions open the side pane directly in split view", async ({ page }) => {
  const actions = page.locator("#titleBar #paneActions");
  await expect(actions).toBeVisible();
  await expect(page.locator("#sidePaneBtn")).toHaveCount(0);
  await expect(page.locator("#panePluginsBtn")).toBeVisible();
  const actionBox = await actions.boundingBox();
  const usageBox = await page.locator("#usagePill").boundingBox();
  const titleBox = await page.locator("#titleBar").boundingBox();
  const minimizeBox = await page.locator("#windowMinimizeBtn").boundingBox();
  const maximizeBox = await page.locator("#windowMaximizeBtn").boundingBox();
  const closeBox = await page.locator("#windowCloseBtn").boundingBox();
  expect(actionBox!.x).toBeGreaterThan(titleBox!.x + titleBox!.width * 0.45);
  expect(usageBox!.x).toBeGreaterThan(titleBox!.x + titleBox!.width * 0.45);
  expect(minimizeBox!.x).toBeGreaterThan(actionBox!.x + actionBox!.width);
  expect(maximizeBox!.x).toBeGreaterThan(minimizeBox!.x);
  expect(closeBox!.x).toBeGreaterThan(maximizeBox!.x);
  expect(closeBox!.x + closeBox!.width).toBeCloseTo(titleBox!.x + titleBox!.width, 0);
  await expect(actions.locator("#paneLayoutBtn [data-icon='layout-split']")).toBeVisible();
  await expect(actions.locator("#paneHistoryBtn [data-icon='rotate-ccw']")).toBeVisible();
  await expect(page.locator("#sidePane")).toBeHidden();
  for (const id of ["paneNewChatBtn", "paneBrowserBtn", "paneTerminalBtn", "paneHistoryBtn", "paneLayoutBtn"]) {
    await expect(actions.locator(`#${id}`)).toBeVisible();
  }
  await expect(page.locator("#titleBar #paneCloseBtn")).toHaveCount(0);
  await actions.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  await page.locator("#sidePane #paneCloseBtn").click();
  await expect(page.locator("#sidePane")).toBeHidden();
  await actions.locator("#paneTerminalBtn").click();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
  await page.locator("#sidePane #paneCloseBtn").click();
  await actions.locator("#paneBrowserBtn").click();
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
});

test("layout button switches layouts and actions remain in the header", async ({ page }) => {
  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator("#agentView")).toHaveClass(/is-tab-layout/);
  await expect(page.locator("#titleBar #paneActions")).toBeVisible();
  const tabsActions = await page.locator("#paneActions").boundingBox();
  const tabsMinimize = await page.locator("#windowMinimizeBtn").boundingBox();
  expect(tabsActions!.x).toBeGreaterThan(page.viewportSize()!.width * 0.45);
  expect(tabsMinimize!.x).toBeGreaterThan(tabsActions!.x + tabsActions!.width);
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#agentMain")).toBeHidden();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  await page.locator("#paneHistoryBtn").click();
  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator("#agentView")).not.toHaveClass(/is-tab-layout/);
});

test("chat mode keeps its title and window controls on the right", async ({ page }) => {
  await page.locator("#chatTab").click();
  await expect(page.locator("#paneActions")).toBeHidden();
  const title = await page.locator("#headerIdentity").boundingBox();
  const windowButtons = await page.locator("#windowMinimizeBtn").boundingBox();
  expect(title!.x).toBeGreaterThan(page.viewportSize()!.width / 2);
  expect(windowButtons!.x).toBeGreaterThan(title!.x + title!.width);
});
