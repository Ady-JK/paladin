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
  await expect(page.locator("#sidePane")).toBeHidden();
  for (const id of ["paneNewChatBtn", "paneBrowserBtn", "paneTerminalBtn", "paneHistoryBtn", "paneCloseBtn"]) {
    await expect(actions.locator(`#${id}`)).toBeVisible();
  }
  await expect(page.locator("#titleBar #paneLayoutBtn")).toBeHidden();
  await actions.locator("#paneNewChatBtn").click();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  await actions.locator("#paneCloseBtn").click();
  await expect(page.locator("#sidePane")).toBeHidden();
  await actions.locator("#paneTerminalBtn").click();
  await expect(page.locator("#sidePane .pane-terminal")).toBeVisible();
  await actions.locator("#paneCloseBtn").click();
  await actions.locator("#paneBrowserBtn").click();
  await expect(page.locator("#sidePane .pane-browser")).toBeVisible();
});

test("history menu switches layouts and actions remain in the header", async ({ page }) => {
  await page.locator("#paneHistoryBtn").click();
  await expect(page.locator("#sidePane")).toBeVisible();
  await expect(page.locator("#paneHistoryMenu")).toBeVisible();
  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator("#agentView")).toHaveClass(/is-tab-layout/);
  await expect(page.locator("#titleBar #paneActions")).toBeVisible();
  await page.locator("#paneNewChatBtn").click();
  await expect(page.locator("#agentMain")).toBeHidden();
  await expect(page.locator("#sidePane .side-chat:visible")).toBeVisible();
  await page.locator("#paneHistoryBtn").click();
  await page.locator("#paneLayoutBtn").click();
  await expect(page.locator("#agentView")).not.toHaveClass(/is-tab-layout/);
});
